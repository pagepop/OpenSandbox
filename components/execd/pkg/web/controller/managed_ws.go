// Copyright 2026 Alibaba Group Holding Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/alibaba/opensandbox/execd/pkg/log"
	"github.com/alibaba/opensandbox/execd/pkg/runtime"
	"github.com/alibaba/opensandbox/execd/pkg/web/model"
)

type managedWSWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *managedWSWriter) json(value any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
	return w.conn.WriteJSON(value)
}

func (w *managedWSWriter) binary(tag byte, offset int64, data []byte) error {
	frame := make([]byte, 9+len(data))
	frame[0] = tag
	binary.BigEndian.PutUint64(frame[1:9], uint64(offset))
	copy(frame[9:], data)
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
	return w.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (w *managedWSWriter) close(code int, message string) {
	w.mu.Lock()
	_ = w.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, message),
		time.Now().Add(time.Second),
	)
	_ = w.conn.Close()
	w.mu.Unlock()
}

// ManagedProcessWebSocket handles the raw managed-process I/O protocol.
func ManagedProcessWebSocket(ctx *gin.Context, manager *runtime.ManagedProcessManager) {
	processID := ctx.Param("processId")
	process, ok := manager.Get(processID)
	if !ok {
		managedWebSocketRequestError(ctx, http.StatusNotFound, runtime.ErrManagedProcessNotFound.Error())
		return
	}
	stdinSequence, err := managedUint64Query(ctx, "stdinSequence")
	if err != nil {
		managedWebSocketRequestError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	stdoutOffset, err := managedOffsetQuery(ctx, "stdoutOffset")
	if err != nil {
		managedWebSocketRequestError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	stderrOffset, err := managedOffsetQuery(ctx, "stderrOffset")
	if err != nil {
		managedWebSocketRequestError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	status := process.Status()
	if stdinSequence > status.StdinSequence || stdoutOffset > status.StdoutOffset || stderrOffset > status.StderrOffset {
		managedWebSocketRequestError(ctx, http.StatusBadRequest, "attachment position is ahead of the server")
		return
	}

	conn, err := wsUpgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		log.Warn("managed process ws upgrade failed for %s: %v", processID, err)
		return
	}
	writer := &managedWSWriter{conn: conn}
	attachment, err := process.AttachStdin(stdinSequence)
	if err != nil {
		managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, err)
		return
	}
	defer attachment.Release()

	status = process.Status()
	if err := writer.json(map[string]any{
		"type":          "connected",
		"processId":     processID,
		"stdinSequence": attachment.StdinSequence(),
		"stdoutOffset":  status.StdoutOffset,
		"stderrOffset":  status.StderrOffset,
	}); err != nil {
		_ = conn.Close()
		return
	}

	streamCtx, cancel := context.WithCancel(ctx.Request.Context())
	defer cancel()
	var workers sync.WaitGroup
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	exitDone := make(chan struct{})
	failOnce := sync.OnceFunc(func() {
		cancel()
		writer.close(websocket.CloseInternalServerErr, model.ManagedWSErrRuntime)
	})
	run := func(done chan<- struct{}, work func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer close(done)
			if err := work(); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("managed process ws failure for %s: %v", processID, err)
				_ = writer.json(map[string]any{"type": "error", "code": model.ManagedWSErrRuntime, "message": err.Error()})
				failOnce()
			}
		}()
	}
	run(stdoutDone, func() error {
		return managedProcessOutputPump(streamCtx, process, runtime.ManagedProcessStdout, stdoutOffset, writer)
	})
	run(stderrDone, func() error {
		return managedProcessOutputPump(streamCtx, process, runtime.ManagedProcessStderr, stderrOffset, writer)
	})
	run(exitDone, func() error {
		select {
		case <-streamCtx.Done():
			return streamCtx.Err()
		case <-process.Done():
			outcome := process.Status()
			return writer.json(map[string]any{
				"type":     "exit",
				"exitCode": outcome.ExitCode,
				"signal":   outcome.Signal,
			})
		}
	})

	takenOver := make(chan struct{})
	go func() {
		select {
		case <-streamCtx.Done():
		case <-attachment.Done():
			writer.close(model.ManagedWSCloseTakenOver, "taken over")
			cancel()
			close(takenOver)
		}
	}()
	go func() {
		select {
		case <-streamCtx.Done():
		case <-stdoutDone:
			select {
			case <-streamCtx.Done():
			case <-stderrDone:
				select {
				case <-streamCtx.Done():
				case <-exitDone:
					writer.close(websocket.CloseNormalClosure, "")
					cancel()
				}
			}
		}
	}()

	processReadLoop(conn, writer, attachment, failOnce, takenOver)
	cancel()
	_ = conn.Close()
	workers.Wait()
}

func managedProcessOutputPump(ctx context.Context, process *runtime.ManagedProcess, stream runtime.ManagedProcessStream, offset int64, writer *managedWSWriter) error {
	for {
		requested := offset
		output, err := process.ReadOutput(ctx, stream, offset)
		if err != nil {
			return err
		}
		if output.Gap {
			if err := writer.json(map[string]any{
				"type":            "gap",
				"stream":          string(stream),
				"requestedOffset": requested,
				"retainedFrom":    output.Offset,
			}); err != nil {
				return err
			}
		}
		if len(output.Data) > 0 {
			tag := model.ManagedBinStdout
			if stream == runtime.ManagedProcessStderr {
				tag = model.ManagedBinStderr
			}
			if err := writer.binary(tag, output.Offset, output.Data); err != nil {
				return err
			}
		}
		offset = output.NextOffset
		if output.EOF {
			return writer.json(map[string]any{
				"type":   string(stream) + "_eof",
				"offset": output.NextOffset,
				"clean":  output.CleanEOF,
			})
		}
	}
}

func processReadLoop(conn *websocket.Conn, writer *managedWSWriter, attachment *runtime.ManagedProcessAttachment, fail func(), takenOver <-chan struct{}) {
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch messageType {
		case websocket.BinaryMessage:
			if len(payload) < 9 || payload[0] != model.ManagedBinStdin {
				managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, errors.New("stdin frame must contain a tag and sequence"))
				return
			}
			sequence := binary.BigEndian.Uint64(payload[1:9])
			ack, err := attachment.WriteStdin(sequence, payload[9:])
			if errors.Is(err, runtime.ErrManagedProcessStdinNotOwner) {
				select {
				case <-takenOver:
				default:
					writer.close(model.ManagedWSCloseTakenOver, "taken over")
				}
				return
			}
			if err != nil {
				managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, err)
				return
			}
			if err := writer.json(map[string]any{"type": "stdin_ack", "sequence": ack}); err != nil {
				fail()
				return
			}
		case websocket.TextMessage:
			var frame struct {
				Type     string  `json:"type"`
				Sequence *uint64 `json:"sequence"`
			}
			if err := json.Unmarshal(payload, &frame); err != nil || frame.Type != "stdin_eof" || frame.Sequence == nil {
				managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, errors.New("invalid stdin EOF frame"))
				return
			}
			ack, err := attachment.CloseStdin(*frame.Sequence)
			if errors.Is(err, runtime.ErrManagedProcessStdinNotOwner) {
				writer.close(model.ManagedWSCloseTakenOver, "taken over")
				return
			}
			if err != nil {
				managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, err)
				return
			}
			if err := writer.json(map[string]any{"type": "stdin_eof", "sequence": ack}); err != nil {
				fail()
				return
			}
		default:
			managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, errors.New("unsupported WebSocket message type"))
			return
		}
	}
}

// ManagedTerminalWebSocket handles raw managed-terminal input and output.
func ManagedTerminalWebSocket(ctx *gin.Context, manager *runtime.ManagedTerminalManager) {
	terminalID := ctx.Param("terminalId")
	terminal, ok := manager.Get(terminalID)
	if !ok {
		managedWebSocketRequestError(ctx, http.StatusNotFound, runtime.ErrManagedTerminalNotFound.Error())
		return
	}
	outputOffset, err := managedOffsetQuery(ctx, "outputOffset")
	if err != nil {
		managedWebSocketRequestError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	status := terminal.Status()
	if outputOffset > status.OutputOffset {
		managedWebSocketRequestError(ctx, http.StatusBadRequest, "attachment position is ahead of the server")
		return
	}

	conn, err := wsUpgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		log.Warn("managed terminal ws upgrade failed for %s: %v", terminalID, err)
		return
	}
	writer := &managedWSWriter{conn: conn}
	status = terminal.Status()
	if err := writer.json(map[string]any{
		"type":         "connected",
		"terminalId":   terminalID,
		"outputOffset": status.OutputOffset,
	}); err != nil {
		_ = conn.Close()
		return
	}

	streamCtx, cancel := context.WithCancel(ctx.Request.Context())
	defer cancel()
	var workers sync.WaitGroup
	outputDone := make(chan struct{})
	exitDone := make(chan struct{})
	failOnce := sync.OnceFunc(func() {
		cancel()
		writer.close(websocket.CloseInternalServerErr, model.ManagedWSErrRuntime)
	})
	run := func(done chan<- struct{}, work func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer close(done)
			if err := work(); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("managed terminal ws failure for %s: %v", terminalID, err)
				_ = writer.json(map[string]any{"type": "error", "code": model.ManagedWSErrRuntime, "message": err.Error()})
				failOnce()
			}
		}()
	}
	run(outputDone, func() error {
		return managedTerminalOutputPump(streamCtx, terminal, outputOffset, writer)
	})
	run(exitDone, func() error {
		select {
		case <-streamCtx.Done():
			return streamCtx.Err()
		case <-terminal.Done():
			outcome := terminal.Status()
			return writer.json(map[string]any{
				"type":     "exit",
				"exitCode": outcome.ExitCode,
				"signal":   outcome.Signal,
			})
		}
	})
	go func() {
		select {
		case <-streamCtx.Done():
		case <-outputDone:
			select {
			case <-streamCtx.Done():
			case <-exitDone:
				writer.close(websocket.CloseNormalClosure, "")
				cancel()
			}
		}
	}()

	terminalReadLoop(conn, writer, terminal, failOnce)
	cancel()
	_ = conn.Close()
	workers.Wait()
}

func managedTerminalOutputPump(ctx context.Context, terminal *runtime.ManagedTerminal, offset int64, writer *managedWSWriter) error {
	for {
		requested := offset
		output, err := terminal.ReadOutput(ctx, offset)
		if err != nil {
			return err
		}
		if output.Gap {
			if err := writer.json(map[string]any{
				"type":            "gap",
				"requestedOffset": requested,
				"retainedFrom":    output.Offset,
			}); err != nil {
				return err
			}
		}
		if len(output.Data) > 0 {
			if err := writer.binary(model.ManagedBinStdout, output.Offset, output.Data); err != nil {
				return err
			}
		}
		offset = output.NextOffset
		if output.EOF {
			return writer.json(map[string]any{"type": "output_eof", "offset": output.NextOffset})
		}
	}
}

func terminalReadLoop(conn *websocket.Conn, writer *managedWSWriter, terminal *runtime.ManagedTerminal, fail func()) {
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch messageType {
		case websocket.BinaryMessage:
			if len(payload) == 0 || payload[0] != model.ManagedBinStdin {
				managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, errors.New("terminal input frame must start with the stdin tag"))
				return
			}
			if _, err := terminal.WriteInput(payload[1:]); err != nil {
				_ = writer.json(map[string]any{"type": "error", "code": model.ManagedWSErrRuntime, "message": err.Error()})
				fail()
				return
			}
		case websocket.TextMessage:
			var frame struct {
				Type string `json:"type"`
				Rows uint16 `json:"rows"`
				Cols uint16 `json:"cols"`
			}
			if err := json.Unmarshal(payload, &frame); err != nil || frame.Type != "resize" || frame.Rows == 0 || frame.Cols == 0 {
				managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, errors.New("invalid terminal resize frame"))
				return
			}
			if err := terminal.Resize(frame.Rows, frame.Cols); err != nil {
				_ = writer.json(map[string]any{"type": "error", "code": model.ManagedWSErrRuntime, "message": err.Error()})
				fail()
				return
			}
		default:
			managedWSFailure(writer, websocket.ClosePolicyViolation, model.ManagedWSErrInvalidFrame, errors.New("unsupported WebSocket message type"))
			return
		}
	}
}

func managedWebSocketRequestError(ctx *gin.Context, status int, message string) {
	ctx.JSON(status, model.ErrorResponse{Code: model.ErrorCodeInvalidRequest, Message: message})
}

func managedWSFailure(writer *managedWSWriter, closeCode int, code string, err error) {
	_ = writer.json(map[string]any{"type": "error", "code": code, "message": err.Error()})
	writer.close(closeCode, code)
}

func managedUint64Query(ctx *gin.Context, name string) (uint64, error) {
	value, ok := ctx.GetQuery(name)
	if !ok {
		return 0, fmt.Errorf("missing query parameter %q", name)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid query parameter %q", name)
	}
	return parsed, nil
}

func managedOffsetQuery(ctx *gin.Context, name string) (int64, error) {
	value, err := managedUint64Query(ctx, name)
	if err != nil {
		return 0, err
	}
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("invalid query parameter %q", name)
	}
	return int64(value), nil
}
