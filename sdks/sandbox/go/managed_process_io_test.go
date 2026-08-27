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
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func managedProcessOutputFrame(frameType byte, offset uint64, data string) []byte {
	frame := make([]byte, 9+len(data))
	frame[0] = frameType
	binary.BigEndian.PutUint64(frame[1:9], offset)
	copy(frame[9:], data)
	return frame
}

func TestManagedProcessAttachmentMapsRawFramesAndOpaqueID(t *testing.T) {
	serverDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/v1/processes/proc%2F1/io" {
			http.Error(w, "unexpected path "+got, http.StatusBadRequest)
			return
		}
		if got := r.Header.Get(execdAuthHeader); got != "token" {
			http.Error(w, "unexpected token "+got, http.StatusUnauthorized)
			return
		}
		query := r.URL.Query()
		if query.Get("stdinSequence") != "4" || query.Get("stdoutOffset") != "1" || query.Get("stderrOffset") != "2" {
			http.Error(w, "unexpected offsets", http.StatusBadRequest)
			return
		}

		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()
		writeJSON := func(value any) error {
			return connection.WriteJSON(value)
		}
		for _, value := range []any{
			map[string]any{"type": "connected", "processId": "proc/1", "stdinSequence": 4, "stdoutOffset": 10, "stderrOffset": 20},
			map[string]any{"type": "gap", "stream": "stdout", "requestedOffset": 1, "retainedFrom": 7},
		} {
			if err := writeJSON(value); err != nil {
				serverDone <- err
				return
			}
		}
		if err := connection.WriteMessage(websocket.BinaryMessage, managedProcessOutputFrame(managedProcessStdoutFrame, 7, "out")); err != nil {
			serverDone <- err
			return
		}
		if err := connection.WriteMessage(websocket.BinaryMessage, managedProcessOutputFrame(managedProcessStderrFrame, 2, "err")); err != nil {
			serverDone <- err
			return
		}

		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			serverDone <- err
			return
		}
		if messageType != websocket.BinaryMessage || len(payload) < 9 || payload[0] != managedProcessStdinFrame || binary.BigEndian.Uint64(payload[1:9]) != 5 || string(payload[9:]) != "input" {
			serverDone <- fmt.Errorf("unexpected stdin frame: type=%d payload=%v", messageType, payload)
			return
		}
		if err := writeJSON(map[string]any{"type": "stdin_ack", "sequence": 5}); err != nil {
			serverDone <- err
			return
		}

		messageType, payload, err = connection.ReadMessage()
		if err != nil {
			serverDone <- err
			return
		}
		var stdinEOF struct {
			Type     string `json:"type"`
			Sequence uint64 `json:"sequence"`
		}
		if messageType != websocket.TextMessage || json.Unmarshal(payload, &stdinEOF) != nil || stdinEOF.Type != "stdin_eof" || stdinEOF.Sequence != 6 {
			serverDone <- fmt.Errorf("unexpected stdin EOF frame: type=%d payload=%q", messageType, payload)
			return
		}
		for _, value := range []any{
			map[string]any{"type": "stdin_eof", "sequence": 6},
			map[string]any{"type": "stdout_eof", "offset": 10, "clean": true},
			map[string]any{"type": "stderr_eof", "offset": 5, "clean": false},
			map[string]any{"type": "exit", "exitCode": nil, "signal": "SIGKILL"},
		} {
			if err := writeJSON(value); err != nil {
				serverDone <- err
				return
			}
		}
		if err := connection.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		); err != nil {
			serverDone <- err
			return
		}
		serverDone <- nil
	}))
	defer server.Close()

	client := NewExecdClient(server.URL, "token")
	attachment, err := client.AttachManagedProcess(context.Background(), "proc/1", ManagedProcessAttachOptions{
		StdinSequence: 4,
		StdoutOffset:  1,
		StderrOffset:  2,
	})
	require.NoError(t, err)

	event, err := attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedProcessIOConnected, event.Type)
	require.Equal(t, "proc/1", event.ProcessID)
	require.Equal(t, uint64(4), event.StdinSequence)

	event, err = attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedProcessIOGap, event.Type)
	require.Equal(t, int64(7), event.RetainedFrom)

	event, err = attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedProcessIOStdout, event.Type)
	require.Equal(t, int64(7), event.Offset)
	require.Equal(t, []byte("out"), event.Data)

	event, err = attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedProcessIOStderr, event.Type)
	require.Equal(t, int64(2), event.Offset)
	require.Equal(t, []byte("err"), event.Data)

	require.NoError(t, attachment.Write(5, []byte("input")))
	event, err = attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedProcessIOStdinAck, event.Type)
	require.Equal(t, uint64(5), event.Sequence)

	require.NoError(t, attachment.CloseStdin(6))
	for _, expectedType := range []ManagedProcessIOEventType{
		ManagedProcessIOStdinEOF,
		ManagedProcessIOStdoutEOF,
		ManagedProcessIOStderrEOF,
		ManagedProcessIOExit,
	} {
		event, err = attachment.Read()
		require.NoError(t, err)
		require.Equal(t, expectedType, event.Type)
	}
	require.Equal(t, "SIGKILL", *event.Signal)
	if event.ExitCode != nil {
		t.Fatal("signal termination should not publish an exit code")
	}
	require.NoError(t, <-serverDone)
}

func TestManagedProcessAttachmentUsesHTTPTransportDialContext(t *testing.T) {
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		defer close(serverDone)
		if err := connection.WriteJSON(map[string]any{
			"type":          "connected",
			"processId":     "proc",
			"stdinSequence": 0,
			"stdoutOffset":  0,
			"stderrOffset":  0,
		}); err != nil {
			return
		}
		_, _, _ = connection.ReadMessage()
	}))
	defer server.Close()

	dialed := make(chan struct{}, 1)
	netDialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			select {
			case dialed <- struct{}{}:
			default:
			}
			return netDialer.DialContext(ctx, network, address)
		},
	}
	defer transport.CloseIdleConnections()
	client := NewExecdClient(
		server.URL,
		"token",
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	attachment, err := client.AttachManagedProcess(
		context.Background(),
		"proc",
		ManagedProcessAttachOptions{},
	)
	require.NoError(t, err)
	select {
	case <-dialed:
	default:
		t.Fatal("managed process attachment bypassed the configured HTTP DialContext")
	}
	event, err := attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedProcessIOConnected, event.Type)
	require.NoError(t, attachment.Close())
	<-serverDone
}
