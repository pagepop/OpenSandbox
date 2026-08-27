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
	"net/http"
	"net/url"
	"time"
)

// ManagedTerminalEnvironment is an environment patch applied over execd's
// scrubbed sandbox environment.
type ManagedTerminalEnvironment = ManagedProcessEnvironment

// ManagedTerminalState reports terminal publication, direct exit, and group quiescence.
type ManagedTerminalState string

const (
	// ManagedTerminalAllocating reserves an operation before publication.
	ManagedTerminalAllocating ManagedTerminalState = "allocating"
	// ManagedTerminalRunning reports a published direct process that has not exited.
	ManagedTerminalRunning ManagedTerminalState = "running"
	// ManagedTerminalExited reports direct-process exit before group quiescence.
	ManagedTerminalExited ManagedTerminalState = "exited"
	// ManagedTerminalQuiescent proves that the terminal process group is empty.
	ManagedTerminalQuiescent ManagedTerminalState = "quiescent"
)

// ManagedTerminalSignal is a signal accepted for the current foreground process group.
type ManagedTerminalSignal string

const (
	// ManagedTerminalSignalInterrupt requests an interactive interrupt.
	ManagedTerminalSignalInterrupt ManagedTerminalSignal = "SIGINT"
	// ManagedTerminalSignalTerminate requests graceful termination.
	ManagedTerminalSignalTerminate ManagedTerminalSignal = "SIGTERM"
	// ManagedTerminalSignalKill requests immediate termination.
	ManagedTerminalSignalKill ManagedTerminalSignal = "SIGKILL"
	// ManagedTerminalSignalStop requests an interactive stop.
	ManagedTerminalSignalStop ManagedTerminalSignal = "SIGTSTP"
	// ManagedTerminalSignalHangup reports terminal disconnection.
	ManagedTerminalSignalHangup ManagedTerminalSignal = "SIGHUP"
)

// CreateManagedTerminalRequest describes one idempotent exact-argv PTY start.
type CreateManagedTerminalRequest struct {
	OperationID string
	Argv        []string
	Cwd         string
	Env         ManagedTerminalEnvironment
	Rows        int
	Cols        int
	// Grace controls TERM-to-KILL escalation. Nil uses execd's default.
	Grace *time.Duration
}

// TerminateManagedTerminalOptions optionally overrides TERM-to-KILL escalation.
type TerminateManagedTerminalOptions struct {
	// Grace nil uses the create-time value. Zero requests immediate SIGKILL.
	Grace *time.Duration
}

// ManagedTerminalStatus separates direct-process outcome from group quiescence.
type ManagedTerminalStatus struct {
	TerminalID         string               `json:"terminalId"`
	PID                *int                 `json:"pid,omitempty"`
	State              ManagedTerminalState `json:"state"`
	ExitCode           *int                 `json:"exitCode"`
	Signal             *string              `json:"signal"`
	TopLevelExited     bool                 `json:"topLevelExited"`
	TreeEmpty          bool                 `json:"treeEmpty"`
	OutputOffset       int64                `json:"outputOffset"`
	OutputRetainedFrom int64                `json:"outputRetainedFrom"`
	OutputEOF          bool                 `json:"outputEof"`
}

// ManagedTerminalReady is published after execd allocates the PTY and starts its process.
type ManagedTerminalReady struct {
	PID int
}

// ManagedTerminalForeground reports the PTY's current foreground process group.
type ManagedTerminalForeground struct {
	ProcessGroup int  `json:"processGroup"`
	InputWaiting bool `json:"inputWaiting"`
}

type createManagedTerminalWireRequest struct {
	OperationID       string                     `json:"operationId"`
	Argv              []string                   `json:"argv"`
	Cwd               string                     `json:"cwd"`
	Env               ManagedTerminalEnvironment `json:"env,omitempty"`
	Rows              int                        `json:"rows"`
	Cols              int                        `json:"cols"`
	GraceMilliseconds *int64                     `json:"graceMs,omitempty"`
}

type terminateManagedTerminalWireRequest struct {
	GraceMilliseconds *int64 `json:"graceMs,omitempty"`
}

type signalManagedTerminalForegroundWireRequest struct {
	Signal ManagedTerminalSignal `json:"signal"`
}

type signalManagedTerminalForegroundWireResponse struct {
	ProcessGroup int `json:"processGroup"`
}

func toCreateManagedTerminalWireRequest(request CreateManagedTerminalRequest) createManagedTerminalWireRequest {
	return createManagedTerminalWireRequest{
		OperationID:       request.OperationID,
		Argv:              request.Argv,
		Cwd:               request.Cwd,
		Env:               request.Env,
		Rows:              request.Rows,
		Cols:              request.Cols,
		GraceMilliseconds: durationMilliseconds(request.Grace),
	}
}

// CreateManagedTerminal idempotently allocates a PTY and starts an exact-argv process.
func (e *ExecdClient) CreateManagedTerminal(ctx context.Context, request CreateManagedTerminalRequest) (*ManagedTerminalStatus, error) {
	var result ManagedTerminalStatus
	err := e.client.doManagedCreate(ctx, "/v1/terminals", toCreateManagedTerminalWireRequest(request), &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetManagedTerminal returns the current terminal status.
func (e *ExecdClient) GetManagedTerminal(ctx context.Context, terminalID string) (*ManagedTerminalStatus, error) {
	var result ManagedTerminalStatus
	path := "/v1/terminals/" + url.PathEscape(terminalID)
	err := e.client.doRequest(ctx, http.MethodGet, path, nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetManagedTerminalForeground returns the current foreground process group.
func (e *ExecdClient) GetManagedTerminalForeground(ctx context.Context, terminalID string) (*ManagedTerminalForeground, error) {
	var result ManagedTerminalForeground
	path := "/v1/terminals/" + url.PathEscape(terminalID) + "/foreground"
	err := e.client.doRequest(ctx, http.MethodGet, path, nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// SignalManagedTerminalForeground signals and returns the current foreground process group.
func (e *ExecdClient) SignalManagedTerminalForeground(ctx context.Context, terminalID string, signal ManagedTerminalSignal) (int, error) {
	path := "/v1/terminals/" + url.PathEscape(terminalID) + "/foreground/signal"
	request := signalManagedTerminalForegroundWireRequest{Signal: signal}
	var result signalManagedTerminalForegroundWireResponse
	if err := e.client.doRequest(ctx, http.MethodPost, path, request, &result); err != nil {
		return 0, err
	}
	return result.ProcessGroup, nil
}

// TerminateManagedTerminal starts or joins group termination and waits for quiescence.
func (e *ExecdClient) TerminateManagedTerminal(ctx context.Context, terminalID string, options *TerminateManagedTerminalOptions) (*ManagedTerminalStatus, error) {
	var request any
	if options != nil {
		request = terminateManagedTerminalWireRequest{
			GraceMilliseconds: durationMilliseconds(options.Grace),
		}
	}
	var result ManagedTerminalStatus
	path := "/v1/terminals/" + url.PathEscape(terminalID) + "/terminate"
	err := e.client.doRequest(ctx, http.MethodPost, path, request, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteManagedTerminal removes a quiescent terminal record and retained output.
func (e *ExecdClient) DeleteManagedTerminal(ctx context.Context, terminalID string) error {
	path := "/v1/terminals/" + url.PathEscape(terminalID)
	return e.client.doRequest(ctx, http.MethodDelete, path, nil, nil)
}
