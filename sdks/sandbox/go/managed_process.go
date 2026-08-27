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

// ManagedProcessEnvironment is an environment patch. A non-nil string sets a
// value and nil removes the named variable from execd's scrubbed environment.
type ManagedProcessEnvironment map[string]*string

// ManagedProcessStdinMode selects the server-side stdin allocation.
type ManagedProcessStdinMode string

const (
	// ManagedProcessStdinPipe allocates a raw stdin pipe.
	ManagedProcessStdinPipe ManagedProcessStdinMode = "pipe"
)

// ManagedProcessState reports process publication, direct exit, and group quiescence.
type ManagedProcessState string

const (
	// ManagedProcessAllocating reserves an operation before publication.
	ManagedProcessAllocating ManagedProcessState = "allocating"
	// ManagedProcessRunning reports a published direct process that has not exited.
	ManagedProcessRunning ManagedProcessState = "running"
	// ManagedProcessExited reports direct-process exit before group quiescence.
	ManagedProcessExited ManagedProcessState = "exited"
	// ManagedProcessQuiescent proves that the managed process group is empty.
	ManagedProcessQuiescent ManagedProcessState = "quiescent"
)

// ResolveExecutableRequest resolves an executable with managed-process environment rules.
type ResolveExecutableRequest struct {
	Executable string                    `json:"executable"`
	Env        ManagedProcessEnvironment `json:"env,omitempty"`
}

// ResolveExecutableResponse contains one validated absolute executable path.
type ResolveExecutableResponse struct {
	Path string `json:"path"`
}

// CreateManagedProcessRequest describes one idempotent exact-argv process start.
type CreateManagedProcessRequest struct {
	OperationID          string
	Argv                 []string
	Cwd                  string
	Env                  ManagedProcessEnvironment
	Stdin                ManagedProcessStdinMode
	StdoutRetentionBytes *int64
	StderrRetentionBytes *int64
	// Grace controls TERM-to-KILL escalation. Nil uses execd's default.
	Grace *time.Duration
}

// TerminateManagedProcessOptions optionally overrides TERM-to-KILL escalation.
type TerminateManagedProcessOptions struct {
	// Grace nil uses the create-time value. Zero requests immediate SIGKILL.
	Grace *time.Duration
}

// ManagedProcessStatus separates direct-process outcome from group quiescence.
type ManagedProcessStatus struct {
	ProcessID          string              `json:"processId"`
	PID                *int                `json:"pid,omitempty"`
	State              ManagedProcessState `json:"state"`
	ExitCode           *int                `json:"exitCode"`
	Signal             *string             `json:"signal"`
	TopLevelExited     bool                `json:"topLevelExited"`
	TreeEmpty          bool                `json:"treeEmpty"`
	StdinSequence      uint64              `json:"stdinSequence"`
	StdoutOffset       int64               `json:"stdoutOffset"`
	StderrOffset       int64               `json:"stderrOffset"`
	StdoutRetainedFrom int64               `json:"stdoutRetainedFrom"`
	StderrRetainedFrom int64               `json:"stderrRetainedFrom"`
	StdoutSpillPath    *string             `json:"stdoutSpillPath"`
	StderrSpillPath    *string             `json:"stderrSpillPath"`
}

// ManagedProcessReady is published after execd starts and identifies a process.
type ManagedProcessReady struct {
	PID int
}

type createManagedProcessWireRequest struct {
	OperationID          string                    `json:"operationId"`
	Argv                 []string                  `json:"argv"`
	Cwd                  string                    `json:"cwd"`
	Env                  ManagedProcessEnvironment `json:"env,omitempty"`
	Stdin                ManagedProcessStdinMode   `json:"stdin"`
	StdoutRetentionBytes *int64                    `json:"stdoutRetentionBytes,omitempty"`
	StderrRetentionBytes *int64                    `json:"stderrRetentionBytes,omitempty"`
	GraceMilliseconds    *int64                    `json:"graceMs,omitempty"`
}

type terminateManagedProcessWireRequest struct {
	GraceMilliseconds *int64 `json:"graceMs,omitempty"`
}

func durationMilliseconds(duration *time.Duration) *int64 {
	if duration == nil {
		return nil
	}
	milliseconds := duration.Milliseconds()
	return &milliseconds
}

func toCreateManagedProcessWireRequest(request CreateManagedProcessRequest) createManagedProcessWireRequest {
	return createManagedProcessWireRequest{
		OperationID:          request.OperationID,
		Argv:                 request.Argv,
		Cwd:                  request.Cwd,
		Env:                  request.Env,
		Stdin:                request.Stdin,
		StdoutRetentionBytes: request.StdoutRetentionBytes,
		StderrRetentionBytes: request.StderrRetentionBytes,
		GraceMilliseconds:    durationMilliseconds(request.Grace),
	}
}

// ResolveExecutable resolves and validates an executable inside the sandbox.
func (e *ExecdClient) ResolveExecutable(ctx context.Context, request ResolveExecutableRequest) (*ResolveExecutableResponse, error) {
	var result ResolveExecutableResponse
	err := e.client.doRequest(ctx, http.MethodPost, "/v1/processes/resolve-executable", request, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateManagedProcess idempotently starts an exact-argv process.
func (e *ExecdClient) CreateManagedProcess(ctx context.Context, request CreateManagedProcessRequest) (*ManagedProcessStatus, error) {
	var result ManagedProcessStatus
	err := e.client.doRequest(ctx, http.MethodPost, "/v1/processes", toCreateManagedProcessWireRequest(request), &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetManagedProcess returns the current process status.
func (e *ExecdClient) GetManagedProcess(ctx context.Context, processID string) (*ManagedProcessStatus, error) {
	var result ManagedProcessStatus
	path := "/v1/processes/" + url.PathEscape(processID)
	err := e.client.doRequest(ctx, http.MethodGet, path, nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// TerminateManagedProcess starts or joins group termination and waits for quiescence.
func (e *ExecdClient) TerminateManagedProcess(ctx context.Context, processID string, options *TerminateManagedProcessOptions) (*ManagedProcessStatus, error) {
	var request any
	if options != nil {
		request = terminateManagedProcessWireRequest{
			GraceMilliseconds: durationMilliseconds(options.Grace),
		}
	}
	var result ManagedProcessStatus
	path := "/v1/processes/" + url.PathEscape(processID) + "/terminate"
	err := e.client.doRequest(ctx, http.MethodPost, path, request, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteManagedProcess removes a quiescent process record and retained output.
func (e *ExecdClient) DeleteManagedProcess(ctx context.Context, processID string) error {
	path := "/v1/processes/" + url.PathEscape(processID)
	return e.client.doRequest(ctx, http.MethodDelete, path, nil, nil)
}
