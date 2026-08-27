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

package opensandbox

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	managedProcessStdinFrame  byte = 0x00
	managedProcessStdoutFrame byte = 0x01
	managedProcessStderrFrame byte = 0x02

	// ManagedProcessCloseTakenOver identifies an attachment replaced by a newer stdin writer.
	ManagedProcessCloseTakenOver = 4001
)

// ManagedProcessAttachOptions selects acknowledged stdin and retained output offsets.
type ManagedProcessAttachOptions struct {
	StdinSequence uint64
	StdoutOffset  int64
	StderrOffset  int64
}

// ManagedProcessIOEventType identifies one decoded managed-process WebSocket frame.
type ManagedProcessIOEventType string

const (
	ManagedProcessIOConnected ManagedProcessIOEventType = "connected"
	ManagedProcessIOStdinAck  ManagedProcessIOEventType = "stdin_ack"
	ManagedProcessIOStdinEOF  ManagedProcessIOEventType = "stdin_eof"
	ManagedProcessIOStdout    ManagedProcessIOEventType = "stdout"
	ManagedProcessIOStderr    ManagedProcessIOEventType = "stderr"
	ManagedProcessIOStdoutEOF ManagedProcessIOEventType = "stdout_eof"
	ManagedProcessIOStderrEOF ManagedProcessIOEventType = "stderr_eof"
	ManagedProcessIOGap       ManagedProcessIOEventType = "gap"
	ManagedProcessIOExit      ManagedProcessIOEventType = "exit"
	ManagedProcessIOError     ManagedProcessIOEventType = "error"
)

// ManagedProcessIOEvent is one decoded binary or control frame. Fields apply
// according to Type; Data is populated only for stdout and stderr frames.
type ManagedProcessIOEvent struct {
	Type            ManagedProcessIOEventType `json:"type"`
	Data            []byte                    `json:"-"`
	ProcessID       string                    `json:"processId,omitempty"`
	StdinSequence   uint64                    `json:"stdinSequence,omitempty"`
	StdoutOffset    int64                     `json:"stdoutOffset,omitempty"`
	StderrOffset    int64                     `json:"stderrOffset,omitempty"`
	Sequence        uint64                    `json:"sequence,omitempty"`
	Offset          int64                     `json:"offset,omitempty"`
	Clean           bool                      `json:"clean,omitempty"`
	Stream          string                    `json:"stream,omitempty"`
	RequestedOffset int64                     `json:"requestedOffset,omitempty"`
	RetainedFrom    int64                     `json:"retainedFrom,omitempty"`
	ExitCode        *int                      `json:"exitCode,omitempty"`
	Signal          *string                   `json:"signal,omitempty"`
	Code            string                    `json:"code,omitempty"`
	Message         string                    `json:"message,omitempty"`
}

// ManagedProcessAttachment is a thin, byte-preserving WebSocket attachment.
// One goroutine may call Read while another calls Write or CloseStdin. Read
// reports wire offsets without tracking continuity; callers own resume cursors.
type ManagedProcessAttachment struct {
	connection *websocket.Conn
	writeMu    sync.Mutex
}

// Read returns the next decoded server frame.
func (a *ManagedProcessAttachment) Read() (*ManagedProcessIOEvent, error) {
	messageType, payload, err := a.connection.ReadMessage()
	if err != nil {
		return nil, err
	}
	switch messageType {
	case websocket.BinaryMessage:
		if len(payload) < 9 {
			return nil, fmt.Errorf("opensandbox: managed process binary frame is shorter than 9 bytes")
		}
		offset := binary.BigEndian.Uint64(payload[1:9])
		if offset > math.MaxInt64 {
			return nil, fmt.Errorf("opensandbox: managed process output offset exceeds int64")
		}
		event := &ManagedProcessIOEvent{
			Offset: int64(offset),
			Data:   append([]byte(nil), payload[9:]...),
		}
		switch payload[0] {
		case managedProcessStdoutFrame:
			event.Type = ManagedProcessIOStdout
		case managedProcessStderrFrame:
			event.Type = ManagedProcessIOStderr
		default:
			return nil, fmt.Errorf("opensandbox: unknown managed process binary frame type 0x%02x", payload[0])
		}
		return event, nil
	case websocket.TextMessage:
		var event ManagedProcessIOEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("opensandbox: decode managed process control frame: %w", err)
		}
		if !knownManagedProcessIOEventType(event.Type) {
			return nil, fmt.Errorf("opensandbox: unknown managed process control frame type %q", event.Type)
		}
		return &event, nil
	default:
		return nil, fmt.Errorf("opensandbox: unexpected managed process WebSocket message type %d", messageType)
	}
}

func knownManagedProcessIOEventType(eventType ManagedProcessIOEventType) bool {
	switch eventType {
	case ManagedProcessIOConnected,
		ManagedProcessIOStdinAck,
		ManagedProcessIOStdinEOF,
		ManagedProcessIOStdoutEOF,
		ManagedProcessIOStderrEOF,
		ManagedProcessIOGap,
		ManagedProcessIOExit,
		ManagedProcessIOError:
		return true
	default:
		return false
	}
}

// Write sends one sequenced stdin byte frame.
func (a *ManagedProcessAttachment) Write(sequence uint64, data []byte) error {
	frame := make([]byte, 9+len(data))
	frame[0] = managedProcessStdinFrame
	binary.BigEndian.PutUint64(frame[1:9], sequence)
	copy(frame[9:], data)
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return a.connection.WriteMessage(websocket.BinaryMessage, frame)
}

// CloseStdin sends explicit stdin EOF at the next sequence number.
func (a *ManagedProcessAttachment) CloseStdin(sequence uint64) error {
	payload, err := json.Marshal(struct {
		Type     string `json:"type"`
		Sequence uint64 `json:"sequence"`
	}{Type: "stdin_eof", Sequence: sequence})
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return a.connection.WriteMessage(websocket.TextMessage, payload)
}

// Close sends a normal WebSocket close frame and releases the connection.
func (a *ManagedProcessAttachment) Close() error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	controlErr := a.connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)
	closeErr := a.connection.Close()
	if controlErr != nil {
		return controlErr
	}
	return closeErr
}

// AttachManagedProcess opens a raw managed-process I/O attachment.
func (e *ExecdClient) AttachManagedProcess(ctx context.Context, processID string, options ManagedProcessAttachOptions) (*ManagedProcessAttachment, error) {
	endpoint, err := url.Parse(e.client.baseURL)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: parse execd URL: %w", err)
	}
	switch endpoint.Scheme {
	case "http":
		endpoint.Scheme = "ws"
	case "https":
		endpoint.Scheme = "wss"
	default:
		return nil, fmt.Errorf("opensandbox: unsupported execd URL scheme %q", endpoint.Scheme)
	}
	escapedPath := strings.TrimSuffix(endpoint.EscapedPath(), "/") +
		"/v1/processes/" + url.PathEscape(processID) + "/io"
	endpoint.Path, err = url.PathUnescape(escapedPath)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: build managed process attachment URL: %w", err)
	}
	endpoint.RawPath = escapedPath
	query := endpoint.Query()
	query.Set("stdinSequence", fmt.Sprintf("%d", options.StdinSequence))
	query.Set("stdoutOffset", fmt.Sprintf("%d", options.StdoutOffset))
	query.Set("stderrOffset", fmt.Sprintf("%d", options.StderrOffset))
	endpoint.RawQuery = query.Encode()

	headers := make(http.Header, len(e.client.headers)+1)
	for key, value := range e.client.headers {
		headers.Set(key, value)
	}
	if e.client.apiKey != "" {
		headers.Set(e.client.authHeader, e.client.apiKey)
	}
	dialer, err := e.client.newWebSocketDialer()
	if err != nil {
		return nil, err
	}
	connection, response, err := dialer.DialContext(ctx, endpoint.String(), headers)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			if response.StatusCode >= http.StatusBadRequest {
				return nil, handleError(response)
			}
		}
		return nil, fmt.Errorf("opensandbox: attach managed process: %w", err)
	}
	return &ManagedProcessAttachment{connection: connection}, nil
}
