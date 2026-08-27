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

//go:build !windows
// +build !windows

package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/alibaba/opensandbox/execd/pkg/runtime"
)

func TestManagedProcessRoutesAreIdempotentAndAcceptEmptyTerminateBody(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	request := map[string]any{
		"operationId":          "route-process",
		"argv":                 []string{"/bin/sh", "-c", "sleep 30"},
		"cwd":                  "/",
		"stdin":                "pipe",
		"stdoutRetentionBytes": 1024,
		"stderrRetentionBytes": 1024,
		"graceMs":              10,
	}
	first := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/processes", request)
	require.Equal(t, http.StatusCreated, first.StatusCode)
	var firstStatus struct {
		ProcessID string `json:"processId"`
	}
	require.NoError(t, json.NewDecoder(first.Body).Decode(&firstStatus))
	first.Body.Close()
	require.NotEmpty(t, firstStatus.ProcessID)

	retry := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/processes", request)
	require.Equal(t, http.StatusOK, retry.StatusCode)
	var retryStatus struct {
		ProcessID string `json:"processId"`
	}
	require.NoError(t, json.NewDecoder(retry.Body).Decode(&retryStatus))
	retry.Body.Close()
	require.Equal(t, firstStatus.ProcessID, retryStatus.ProcessID)

	request["argv"] = []string{"/bin/sh", "-c", "exit 0"}
	conflict := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/processes", request)
	require.Equal(t, http.StatusConflict, conflict.StatusCode)
	conflict.Body.Close()

	terminate, err := http.Post(server.URL+"/v1/processes/"+firstStatus.ProcessID+"/terminate", "application/json", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, terminate.StatusCode)
	terminate.Body.Close()

	deleteRequest, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/processes/"+firstStatus.ProcessID, nil)
	require.NoError(t, err)
	deleted, err := http.DefaultClient.Do(deleteRequest)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, deleted.StatusCode)
	deleted.Body.Close()

	request["argv"] = []string{"/bin/sh", "-c", "sleep 30"}
	deletedRetry := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/processes", request)
	require.Equal(t, http.StatusConflict, deletedRetry.StatusCode)
	deletedRetry.Body.Close()
}

func TestManagedTerminalRoutesExposeForegroundAndNoContentMutations(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	request := map[string]any{
		"operationId": "route-terminal",
		"argv":        []string{"/bin/sh", "-c", "sleep 30"},
		"cwd":         "/",
		"rows":        24,
		"cols":        80,
		"graceMs":     10,
	}
	created := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/terminals", request)
	require.Equal(t, http.StatusCreated, created.StatusCode)
	var status struct {
		TerminalID string `json:"terminalId"`
	}
	require.NoError(t, json.NewDecoder(created.Body).Decode(&status))
	created.Body.Close()
	require.NotEmpty(t, status.TerminalID)

	foreground, err := http.Get(server.URL + "/v1/terminals/" + status.TerminalID + "/foreground")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, foreground.StatusCode)
	var foregroundBody map[string]any
	require.NoError(t, json.NewDecoder(foreground.Body).Decode(&foregroundBody))
	foreground.Body.Close()
	require.Contains(t, foregroundBody, "processGroup")
	require.Contains(t, foregroundBody, "inputWaiting")

	invalidSignal := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/terminals/"+status.TerminalID+"/foreground/signal", map[string]any{"signal": "SIGUSR1"})
	require.Equal(t, http.StatusBadRequest, invalidSignal.StatusCode)
	invalidSignal.Body.Close()

	signal := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/terminals/"+status.TerminalID+"/foreground/signal", map[string]any{"signal": "SIGINT"})
	require.Equal(t, http.StatusNoContent, signal.StatusCode)
	require.Equal(t, int64(0), signal.ContentLength)
	signal.Body.Close()

	terminate, err := http.Post(server.URL+"/v1/terminals/"+status.TerminalID+"/terminate", "application/json", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, terminate.StatusCode)
	terminate.Body.Close()

	deleteRequest, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/terminals/"+status.TerminalID, nil)
	require.NoError(t, err)
	deleted, err := http.DefaultClient.Do(deleteRequest)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, deleted.StatusCode)
	deleted.Body.Close()

	deletedRetry := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/terminals", request)
	require.Equal(t, http.StatusConflict, deletedRetry.StatusCode)
	deletedRetry.Body.Close()
}

func TestManagedCreateRejectsMissingExecutable(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	process := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/processes", map[string]any{
		"operationId": "missing-process-executable",
		"argv":        []string{"/definitely/not/an/executable"},
		"cwd":         "/",
		"stdin":       "pipe",
	})
	require.Equal(t, http.StatusBadRequest, process.StatusCode)
	process.Body.Close()

	terminal := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/terminals", map[string]any{
		"operationId": "missing-terminal-executable",
		"argv":        []string{"/definitely/not/an/executable"},
		"cwd":         "/",
		"rows":        24,
		"cols":        80,
	})
	require.Equal(t, http.StatusBadRequest, terminal.StatusCode)
	terminal.Body.Close()
}

func TestManagedTerminalForegroundReturnsConflictAfterNaturalExit(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	created := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/terminals", map[string]any{
		"operationId": "inactive-terminal",
		"argv":        []string{"/bin/sh", "-c", "exit 0"},
		"cwd":         "/",
		"rows":        24,
		"cols":        80,
	})
	require.Equal(t, http.StatusCreated, created.StatusCode)
	var status struct {
		TerminalID string `json:"terminalId"`
	}
	require.NoError(t, json.NewDecoder(created.Body).Decode(&status))
	created.Body.Close()
	terminal, ok := terminals.Get(status.TerminalID)
	require.True(t, ok)
	select {
	case <-terminal.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not exit")
	}

	foreground, err := http.Get(server.URL + "/v1/terminals/" + status.TerminalID + "/foreground")
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, foreground.StatusCode)
	foreground.Body.Close()

	signal := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/terminals/"+status.TerminalID+"/foreground/signal", map[string]any{"signal": "SIGINT"})
	require.Equal(t, http.StatusConflict, signal.StatusCode)
	signal.Body.Close()
}

func TestManagedCreateRejectsGraceOverflow(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	response := managedJSONRequest(t, http.MethodPost, server.URL+"/v1/processes", map[string]any{
		"operationId": "overflow",
		"argv":        []string{"/bin/true"},
		"cwd":         "/",
		"stdin":       "pipe",
		"graceMs":     maxManagedGraceMSForTest + 1,
	})
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	response.Body.Close()
}

const maxManagedGraceMSForTest int64 = (1<<63 - 1) / int64(time.Millisecond)

func TestManagedProcessWebSocketProtocol(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	process, _, err := processes.Create(runtime.ManagedProcessRequest{
		OperationID:          "process-ws",
		Argv:                 []string{"/bin/sh", "-c", "cat; printf err >&2"},
		Cwd:                  "/",
		StdoutRetentionBytes: 1024,
		StderrRetentionBytes: 1024,
		Grace:                time.Second,
	})
	require.NoError(t, err)
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	connection := dialManagedWebSocket(t, server.URL, fmt.Sprintf(
		"/v1/processes/%s/io?stdinSequence=0&stdoutOffset=0&stderrOffset=0", process.ID(),
	))
	defer connection.Close()
	connected := readManagedControl(t, connection)
	require.Equal(t, "connected", connected["type"])
	require.EqualValues(t, 0, connected["stdinSequence"])
	require.Contains(t, connected, "stdoutOffset")
	require.Contains(t, connected, "stderrOffset")

	stdin := []byte{'a', 0, 'b', '\n'}
	frame := make([]byte, 9+len(stdin))
	frame[0] = 0x00
	binary.BigEndian.PutUint64(frame[1:9], 1)
	copy(frame[9:], stdin)
	require.NoError(t, connection.WriteMessage(websocket.BinaryMessage, frame))
	require.NoError(t, connection.WriteJSON(map[string]any{"type": "stdin_eof", "sequence": 2}))

	var stdout, stderr []byte
	seen := map[string]bool{}
	for !(seen["stdin_ack"] && seen["stdin_eof"] && seen["stdout_eof"] && seen["stderr_eof"] && seen["exit"]) {
		messageType, payload, err := connection.ReadMessage()
		require.NoError(t, err)
		if messageType == websocket.BinaryMessage {
			require.GreaterOrEqual(t, len(payload), 9)
			switch payload[0] {
			case 0x01:
				stdout = append(stdout, payload[9:]...)
			case 0x02:
				stderr = append(stderr, payload[9:]...)
			default:
				t.Fatalf("unexpected binary frame tag 0x%x", payload[0])
			}
			continue
		}
		var control map[string]any
		require.NoError(t, json.Unmarshal(payload, &control))
		typeName, _ := control["type"].(string)
		seen[typeName] = true
		if typeName == "exit" {
			require.Contains(t, control, "exitCode")
			require.Contains(t, control, "signal")
		}
	}
	require.Equal(t, stdin, stdout)
	require.Equal(t, []byte("err"), stderr)
}

func TestManagedProcessWebSocketTakeoverClosesPriorAttachment(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	process, _, err := processes.Create(runtime.ManagedProcessRequest{
		OperationID:          "process-takeover",
		Argv:                 []string{"/bin/sh", "-c", "sleep 30"},
		Cwd:                  "/",
		StdoutRetentionBytes: 1024,
		StderrRetentionBytes: 1024,
		Grace:                time.Second,
	})
	require.NoError(t, err)
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	path := fmt.Sprintf("/v1/processes/%s/io?stdinSequence=0&stdoutOffset=0&stderrOffset=0", process.ID())
	first := dialManagedWebSocket(t, server.URL, path)
	defer first.Close()
	require.Equal(t, "connected", readManagedControl(t, first)["type"])
	second := dialManagedWebSocket(t, server.URL, path)
	defer second.Close()
	require.Equal(t, "connected", readManagedControl(t, second)["type"])

	_, _, err = first.ReadMessage()
	var closeError *websocket.CloseError
	require.ErrorAs(t, err, &closeError)
	require.Equal(t, 4001, closeError.Code)
}

func TestManagedTerminalWebSocketProtocol(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	terminal, _, err := terminals.Create(runtime.ManagedTerminalRequest{
		OperationID: "terminal-ws",
		Argv:        []string{"/bin/cat"},
		Cwd:         "/",
		Rows:        24,
		Cols:        80,
		Grace:       time.Second,
	})
	require.NoError(t, err)
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	connection := dialManagedWebSocket(t, server.URL, fmt.Sprintf(
		"/v1/terminals/%s/io?outputOffset=0", terminal.ID(),
	))
	defer connection.Close()
	connected := readManagedControl(t, connection)
	require.Equal(t, "connected", connected["type"])
	require.Contains(t, connected, "outputOffset")
	require.NoError(t, connection.WriteJSON(map[string]any{"type": "resize", "rows": 40, "cols": 120}))
	require.NoError(t, connection.WriteMessage(websocket.BinaryMessage, append([]byte{0x00}, []byte("terminal-input")...)))

	foundInput := false
	for !foundInput {
		messageType, payload, err := connection.ReadMessage()
		require.NoError(t, err)
		if messageType == websocket.BinaryMessage {
			require.GreaterOrEqual(t, len(payload), 9)
			require.Equal(t, byte(0x01), payload[0])
			foundInput = bytes.Contains(payload[9:], []byte("terminal-input"))
		}
	}

	terminated := make(chan error, 1)
	zero := time.Duration(0)
	go func() {
		_, err := terminals.Terminate(context.Background(), terminal.ID(), &zero)
		terminated <- err
	}()
	seenExit := false
	seenEOF := false
	for !seenExit || !seenEOF {
		messageType, payload, err := connection.ReadMessage()
		require.NoError(t, err)
		if messageType != websocket.TextMessage {
			continue
		}
		var control map[string]any
		require.NoError(t, json.Unmarshal(payload, &control))
		switch control["type"] {
		case "exit":
			seenExit = true
			require.Contains(t, control, "exitCode")
			require.Contains(t, control, "signal")
		case "output_eof":
			seenEOF = true
			require.Contains(t, control, "offset")
		}
	}
	require.NoError(t, <-terminated)
}

func TestManagedWebSocketRejectsInvalidFrameWithPolicyClose(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	process, _, err := processes.Create(runtime.ManagedProcessRequest{
		OperationID:          "process-invalid-frame",
		Argv:                 []string{"/bin/sh", "-c", "sleep 30"},
		Cwd:                  "/",
		StdoutRetentionBytes: 1024,
		StderrRetentionBytes: 1024,
		Grace:                time.Second,
	})
	require.NoError(t, err)
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	connection := dialManagedWebSocket(t, server.URL, fmt.Sprintf(
		"/v1/processes/%s/io?stdinSequence=0&stdoutOffset=0&stderrOffset=0", process.ID(),
	))
	defer connection.Close()
	readManagedControl(t, connection)
	require.NoError(t, connection.WriteMessage(websocket.BinaryMessage, []byte{0xff}))
	errorFrame := readManagedControl(t, connection)
	require.Equal(t, "error", errorFrame["type"])
	require.Equal(t, "INVALID_FRAME", errorFrame["code"])
	_, _, err = connection.ReadMessage()
	var closeError *websocket.CloseError
	require.ErrorAs(t, err, &closeError)
	require.Equal(t, websocket.ClosePolicyViolation, closeError.Code)
}

func TestManagedProcessWebSocketRejectsMissingEOFSequence(t *testing.T) {
	processes := runtime.NewManagedProcessManager()
	terminals := runtime.NewManagedTerminalManager()
	process, _, err := processes.Create(runtime.ManagedProcessRequest{
		OperationID:          "process-missing-eof-sequence",
		Argv:                 []string{"/bin/sh", "-c", "sleep 30"},
		Cwd:                  "/",
		StdoutRetentionBytes: 1024,
		StderrRetentionBytes: 1024,
		Grace:                time.Second,
	})
	require.NoError(t, err)
	server := httptest.NewServer(NewRouter("", processes, terminals))
	defer server.Close()
	defer shutdownManagedManagers(t, processes, terminals)

	connection := dialManagedWebSocket(t, server.URL, fmt.Sprintf(
		"/v1/processes/%s/io?stdinSequence=0&stdoutOffset=0&stderrOffset=0", process.ID(),
	))
	defer connection.Close()
	readManagedControl(t, connection)
	require.NoError(t, connection.WriteJSON(map[string]any{"type": "stdin_eof"}))
	errorFrame := readManagedControl(t, connection)
	require.Equal(t, "error", errorFrame["type"])
	require.Equal(t, "INVALID_FRAME", errorFrame["code"])
	_, _, err = connection.ReadMessage()
	var closeError *websocket.CloseError
	require.ErrorAs(t, err, &closeError)
	require.Equal(t, websocket.ClosePolicyViolation, closeError.Code)
}

func managedJSONRequest(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	request, err := http.NewRequest(method, url, bytes.NewReader(payload))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	return response
}

func dialManagedWebSocket(t *testing.T, serverURL, path string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(serverURL, "http") + path
	connection, response, err := websocket.DefaultDialer.Dial(url, nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
		if err != nil {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("dial %s: %v: %s", url, err, body)
		}
	}
	require.NoError(t, err)
	require.NoError(t, connection.SetReadDeadline(time.Now().Add(5*time.Second)))
	return connection
}

func readManagedControl(t *testing.T, connection *websocket.Conn) map[string]any {
	t.Helper()
	messageType, payload, err := connection.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	var frame map[string]any
	require.NoError(t, json.Unmarshal(payload, &frame))
	return frame
}

func shutdownManagedManagers(t *testing.T, processes *runtime.ManagedProcessManager, terminals *runtime.ManagedTerminalManager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, processes.Shutdown(ctx))
	require.NoError(t, terminals.Shutdown(ctx))
}
