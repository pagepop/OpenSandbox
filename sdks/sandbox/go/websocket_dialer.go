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
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

func cloneWebSocketDialer(source *websocket.Dialer) *websocket.Dialer {
	dialer := *source
	if source.TLSClientConfig != nil {
		dialer.TLSClientConfig = source.TLSClientConfig.Clone()
	}
	return &dialer
}

func (c *Client) newWebSocketDialer() (*websocket.Dialer, error) {
	if c.webSocketDialer != nil {
		return cloneWebSocketDialer(c.webSocketDialer), nil
	}
	transport, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("opensandbox: custom HTTP transport requires WithWebSocketDialer for WebSocket attachments")
	}
	if transport.DialTLS != nil || transport.DialTLSContext != nil {
		return nil, fmt.Errorf("opensandbox: custom HTTP TLS dial functions require WithWebSocketDialer for WebSocket attachments")
	}

	dialer := cloneWebSocketDialer(websocket.DefaultDialer)
	dialer.NetDial = transport.Dial
	dialer.NetDialContext = transport.DialContext
	dialer.Proxy = transport.Proxy
	dialer.HandshakeTimeout = transport.TLSHandshakeTimeout
	if transport.TLSClientConfig != nil {
		dialer.TLSClientConfig = transport.TLSClientConfig.Clone()
	} else {
		dialer.TLSClientConfig = nil
	}
	dialer.Jar = c.httpClient.Jar
	return dialer, nil
}
