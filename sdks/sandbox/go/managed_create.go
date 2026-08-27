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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// doManagedCreate replays the same encoded request once when publication may
// have succeeded but its success response was lost in transport.
func (c *Client) doManagedCreate(ctx context.Context, path string, body, result any) error {
	requestBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("opensandbox: marshal request: %w", err)
	}

	recoverable, err := c.doManagedCreateOnce(ctx, path, requestBody, result)
	if err == nil || !recoverable || ctx.Err() != nil {
		return err
	}
	_, err = c.doManagedCreateOnce(ctx, path, requestBody, result)
	return err
}

func (c *Client) doManagedCreateOnce(ctx context.Context, path string, body []byte, result any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("opensandbox: create request: %w", err)
	}
	req.Header.Set("User-Agent", "OpenSandbox-Go-SDK/"+Version)
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	if c.apiKey != "" {
		req.Header.Set(c.authHeader, c.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if resp != nil {
			if resp.Body != nil {
				resp.Body.Close()
			}
			return false, fmt.Errorf("opensandbox: do request: %w", err)
		}
		return true, fmt.Errorf("opensandbox: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return false, handleError(resp)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		recoverable := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
		return recoverable, fmt.Errorf("opensandbox: read response: %w", err)
	}
	if err := json.Unmarshal(responseBody, result); err != nil {
		return false, fmt.Errorf("opensandbox: decode response: %w", err)
	}
	return false, nil
}
