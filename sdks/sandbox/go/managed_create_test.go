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
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type managedCreateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f managedCreateRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type interruptedManagedCreateBody struct{}

func (*interruptedManagedCreateBody) Read(buffer []byte) (int, error) {
	return copy(buffer, `{"terminalId":"term-1"`), io.ErrUnexpectedEOF
}

func (*interruptedManagedCreateBody) Close() error { return nil }

func managedCreateResponse(status int, body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       body,
	}
}

func managedCreateClient(roundTrip managedCreateRoundTripFunc, opts ...Option) *ExecdClient {
	options := append([]Option{WithHTTPClient(&http.Client{Transport: roundTrip})}, opts...)
	return NewExecdClient("http://execd.test", "token", options...)
}

func TestManagedProcessCreateRetriesTransportFailureWithSameBody(t *testing.T) {
	var bodies [][]byte
	client := managedCreateClient(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		bodies = append(bodies, bytes.Clone(body))
		if len(bodies) == 1 {
			return nil, errors.New("connection reset")
		}
		return managedCreateResponse(
			http.StatusCreated,
			io.NopCloser(strings.NewReader(`{"processId":"proc-1","pid":4321,"state":"running"}`)),
		), nil
	})

	status, err := client.CreateManagedProcess(context.Background(), CreateManagedProcessRequest{
		OperationID: "operation-1",
		Argv:        []string{"/bin/echo", "hello"},
		Cwd:         "/workspace",
		Stdin:       ManagedProcessStdinPipe,
	})

	require.NoError(t, err)
	require.Equal(t, "proc-1", status.ProcessID)
	require.Len(t, bodies, 2)
	require.Equal(t, string(bodies[0]), string(bodies[1]))
	assert.Contains(t, string(bodies[0]), `"operationId":"operation-1"`)
}

func TestManagedTerminalCreateRetriesInterruptedSuccessBody(t *testing.T) {
	calls := 0
	client := managedCreateClient(func(_ *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return managedCreateResponse(http.StatusCreated, &interruptedManagedCreateBody{}), nil
		}
		return managedCreateResponse(
			http.StatusCreated,
			io.NopCloser(strings.NewReader(`{"terminalId":"term-1","pid":5432,"state":"running"}`)),
		), nil
	})

	status, err := client.CreateManagedTerminal(context.Background(), CreateManagedTerminalRequest{
		OperationID: "operation-1",
		Argv:        []string{"/bin/sh"},
		Cwd:         "/workspace",
		Rows:        24,
		Cols:        80,
	})

	require.NoError(t, err)
	require.Equal(t, "term-1", status.TerminalID)
	require.Equal(t, 2, calls)
}

func TestManagedCreateDoesNotRetryHTTPError(t *testing.T) {
	for _, statusCode := range []int{http.StatusBadRequest, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			calls := 0
			client := managedCreateClient(func(_ *http.Request) (*http.Response, error) {
				calls++
				return managedCreateResponse(
					statusCode,
					io.NopCloser(strings.NewReader(`{"code":"failed","message":"not created"}`)),
				), nil
			}, WithRetry(DefaultRetryConfig()))

			_, err := client.CreateManagedProcess(context.Background(), CreateManagedProcessRequest{
				OperationID: "operation-http-error",
				Argv:        []string{"/bin/true"},
				Cwd:         "/workspace",
				Stdin:       ManagedProcessStdinPipe,
			})

			var apiError *APIError
			require.ErrorAs(t, err, &apiError)
			require.Equal(t, statusCode, apiError.StatusCode)
			require.Equal(t, 1, calls)
		})
	}
}

func TestManagedCreateDoesNotRetryCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	client := managedCreateClient(func(request *http.Request) (*http.Response, error) {
		calls++
		cancel()
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	_, err := client.CreateManagedTerminal(ctx, CreateManagedTerminalRequest{
		OperationID: "operation-canceled",
		Argv:        []string{"/bin/sh"},
		Cwd:         "/workspace",
		Rows:        24,
		Cols:        80,
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}

func TestManagedCreateDoesNotRetryJSONDecodeError(t *testing.T) {
	calls := 0
	client := managedCreateClient(func(_ *http.Request) (*http.Response, error) {
		calls++
		return managedCreateResponse(
			http.StatusCreated,
			io.NopCloser(strings.NewReader(`{"processId":`)),
		), nil
	})

	_, err := client.CreateManagedProcess(context.Background(), CreateManagedProcessRequest{
		OperationID: "operation-invalid-json",
		Argv:        []string{"/bin/true"},
		Cwd:         "/workspace",
		Stdin:       ManagedProcessStdinPipe,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode response")
	require.Equal(t, 1, calls)
}

func TestManagedCreateRetriesOnlyOnce(t *testing.T) {
	calls := 0
	client := managedCreateClient(func(_ *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("connection reset")
	})

	_, err := client.CreateManagedTerminal(context.Background(), CreateManagedTerminalRequest{
		OperationID: "operation-unavailable",
		Argv:        []string{"/bin/sh"},
		Cwd:         "/workspace",
		Rows:        24,
		Cols:        80,
	})

	require.Error(t, err)
	require.Equal(t, 2, calls)
}
