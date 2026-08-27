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
	managedTerminalInputFrame  byte = 0x00
	managedTerminalOutputFrame byte = 0x01
)

// ManagedTerminalAttachOptions selects the retained merged-output offset.
type ManagedTerminalAttachOptions struct {
	OutputOffset int64
}

// ManagedTerminalIOEventType identifies one decoded managed-terminal WebSocket frame.
type ManagedTerminalIOEventType string

const (
	// ManagedTerminalIOConnected publishes the terminal identity and current output tail.
	ManagedTerminalIOConnected ManagedTerminalIOEventType = "connected"
	// ManagedTerminalIOOutput carries merged PTY output bytes and their start offset.
	ManagedTerminalIOOutput ManagedTerminalIOEventType = "output"
	// ManagedTerminalIOGap reports output discarded before the retained range.
	ManagedTerminalIOGap ManagedTerminalIOEventType = "gap"
	// ManagedTerminalIOOutputEOF reports the final merged-output offset.
	ManagedTerminalIOOutputEOF ManagedTerminalIOEventType = "output_eof"
	// ManagedTerminalIOExit reports the direct process outcome.
	ManagedTerminalIOExit ManagedTerminalIOEventType = "exit"
	// ManagedTerminalIOError reports a server-side attachment failure.
	ManagedTerminalIOError ManagedTerminalIOEventType = "error"
)

// ManagedTerminalIOEvent is one decoded binary or control frame. Data is
// populated only for output frames. Read reports offsets without tracking
// continuity; callers own resume cursors.
type ManagedTerminalIOEvent struct {
	Type            ManagedTerminalIOEventType `json:"type"`
	Data            []byte                     `json:"-"`
	TerminalID      string                     `json:"terminalId,omitempty"`
	OutputOffset    int64                      `json:"outputOffset,omitempty"`
	Offset          int64                      `json:"offset,omitempty"`
	RequestedOffset int64                      `json:"requestedOffset,omitempty"`
	RetainedFrom    int64                      `json:"retainedFrom,omitempty"`
	ExitCode        *int                       `json:"exitCode,omitempty"`
	Signal          *string                    `json:"signal,omitempty"`
	Code            string                     `json:"code,omitempty"`
	Message         string                     `json:"message,omitempty"`
}

// ManagedTerminalAttachment is a thin, byte-preserving WebSocket attachment.
// One goroutine may call Read while another calls Write, Resize, or Close.
type ManagedTerminalAttachment struct {
	connection *websocket.Conn
	writeMu    sync.Mutex
}

// Read returns the next decoded server frame.
func (a *ManagedTerminalAttachment) Read() (*ManagedTerminalIOEvent, error) {
	messageType, payload, err := a.connection.ReadMessage()
	if err != nil {
		return nil, err
	}
	switch messageType {
	case websocket.BinaryMessage:
		if len(payload) < 9 {
			return nil, fmt.Errorf("opensandbox: managed terminal binary frame is shorter than 9 bytes")
		}
		if payload[0] != managedTerminalOutputFrame {
			return nil, fmt.Errorf("opensandbox: unknown managed terminal binary frame type 0x%02x", payload[0])
		}
		offset := binary.BigEndian.Uint64(payload[1:9])
		if offset > math.MaxInt64 {
			return nil, fmt.Errorf("opensandbox: managed terminal output offset exceeds int64")
		}
		return &ManagedTerminalIOEvent{
			Type:   ManagedTerminalIOOutput,
			Offset: int64(offset),
			Data:   append([]byte(nil), payload[9:]...),
		}, nil
	case websocket.TextMessage:
		var event ManagedTerminalIOEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("opensandbox: decode managed terminal control frame: %w", err)
		}
		if !knownManagedTerminalIOEventType(event.Type) {
			return nil, fmt.Errorf("opensandbox: unknown managed terminal control frame type %q", event.Type)
		}
		return &event, nil
	default:
		return nil, fmt.Errorf("opensandbox: unexpected managed terminal WebSocket message type %d", messageType)
	}
}

func knownManagedTerminalIOEventType(eventType ManagedTerminalIOEventType) bool {
	switch eventType {
	case ManagedTerminalIOConnected,
		ManagedTerminalIOGap,
		ManagedTerminalIOOutputEOF,
		ManagedTerminalIOExit,
		ManagedTerminalIOError:
		return true
	default:
		return false
	}
}

// Write sends terminal input bytes.
func (a *ManagedTerminalAttachment) Write(data []byte) error {
	frame := make([]byte, 1+len(data))
	frame[0] = managedTerminalInputFrame
	copy(frame[1:], data)
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return a.connection.WriteMessage(websocket.BinaryMessage, frame)
}

// Resize changes the terminal window size.
func (a *ManagedTerminalAttachment) Resize(rows, cols int) error {
	if rows < 1 || rows > 65535 {
		return fmt.Errorf("opensandbox: managed terminal rows must be from 1 through 65535")
	}
	if cols < 1 || cols > 65535 {
		return fmt.Errorf("opensandbox: managed terminal cols must be from 1 through 65535")
	}
	payload, err := json.Marshal(struct {
		Type string `json:"type"`
		Rows int    `json:"rows"`
		Cols int    `json:"cols"`
	}{Type: "resize", Rows: rows, Cols: cols})
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return a.connection.WriteMessage(websocket.TextMessage, payload)
}

// Close sends a normal WebSocket close frame and releases the connection.
func (a *ManagedTerminalAttachment) Close() error {
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

// AttachManagedTerminal opens raw managed-terminal I/O.
func (e *ExecdClient) AttachManagedTerminal(ctx context.Context, terminalID string, options ManagedTerminalAttachOptions) (*ManagedTerminalAttachment, error) {
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
		"/v1/terminals/" + url.PathEscape(terminalID) + "/io"
	endpoint.Path, err = url.PathUnescape(escapedPath)
	if err != nil {
		return nil, fmt.Errorf("opensandbox: build managed terminal attachment URL: %w", err)
	}
	endpoint.RawPath = escapedPath
	query := endpoint.Query()
	query.Set("outputOffset", fmt.Sprintf("%d", options.OutputOffset))
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
		return nil, fmt.Errorf("opensandbox: attach managed terminal: %w", err)
	}
	return &ManagedTerminalAttachment{connection: connection}, nil
}
