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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func makeManagedTerminalOutputFrame(offset uint64, data string) []byte {
	frame := make([]byte, 9+len(data))
	frame[0] = managedTerminalOutputFrame
	binary.BigEndian.PutUint64(frame[1:9], offset)
	copy(frame[9:], data)
	return frame
}

func TestManagedTerminalAttachmentMapsRawFramesAndOpaqueID(t *testing.T) {
	serverDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/v1/terminals/term%2F1/io" {
			http.Error(w, "unexpected path "+got, http.StatusBadRequest)
			return
		}
		if got := r.Header.Get(execdAuthHeader); got != "token" {
			http.Error(w, "unexpected token "+got, http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("outputOffset"); got != "3" {
			http.Error(w, "unexpected output offset "+got, http.StatusBadRequest)
			return
		}

		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()
		for _, value := range []any{
			map[string]any{"type": "connected", "terminalId": "term/1", "outputOffset": 10},
			map[string]any{"type": "gap", "requestedOffset": 3, "retainedFrom": 7},
		} {
			if err := connection.WriteJSON(value); err != nil {
				serverDone <- err
				return
			}
		}
		if err := connection.WriteMessage(websocket.BinaryMessage, makeManagedTerminalOutputFrame(7, "out")); err != nil {
			serverDone <- err
			return
		}

		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			serverDone <- err
			return
		}
		if messageType != websocket.BinaryMessage || len(payload) < 1 || payload[0] != managedTerminalInputFrame || string(payload[1:]) != "input" {
			serverDone <- fmt.Errorf("unexpected terminal input frame: type=%d payload=%v", messageType, payload)
			return
		}

		messageType, payload, err = connection.ReadMessage()
		if err != nil {
			serverDone <- err
			return
		}
		var resize struct {
			Type string `json:"type"`
			Rows int    `json:"rows"`
			Cols int    `json:"cols"`
		}
		if messageType != websocket.TextMessage || json.Unmarshal(payload, &resize) != nil || resize.Type != "resize" || resize.Rows != 50 || resize.Cols != 140 {
			serverDone <- fmt.Errorf("unexpected terminal resize frame: type=%d payload=%q", messageType, payload)
			return
		}
		for _, value := range []any{
			map[string]any{"type": "output_eof", "offset": 10},
			map[string]any{"type": "exit", "exitCode": nil, "signal": "SIGKILL"},
		} {
			if err := connection.WriteJSON(value); err != nil {
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
	attachment, err := client.AttachManagedTerminal(
		context.Background(),
		"term/1",
		ManagedTerminalAttachOptions{OutputOffset: 3},
	)
	require.NoError(t, err)

	event, err := attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedTerminalIOConnected, event.Type)
	require.Equal(t, "term/1", event.TerminalID)
	event, err = attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedTerminalIOGap, event.Type)
	require.Equal(t, int64(7), event.RetainedFrom)
	event, err = attachment.Read()
	require.NoError(t, err)
	require.Equal(t, ManagedTerminalIOOutput, event.Type)
	require.Equal(t, int64(7), event.Offset)
	require.Equal(t, []byte("out"), event.Data)

	require.NoError(t, attachment.Write([]byte("input")))
	require.NoError(t, attachment.Resize(50, 140))
	for _, expectedType := range []ManagedTerminalIOEventType{ManagedTerminalIOOutputEOF, ManagedTerminalIOExit} {
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

func TestManagedTerminalAttachmentRejectsInvalidResize(t *testing.T) {
	attachment := &ManagedTerminalAttachment{}
	require.Error(t, attachment.Resize(0, 80))
	require.Error(t, attachment.Resize(24, 65536))
}
