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

package model

// ManagedEnvironment is a sandbox-side environment patch. Nil removes a name.
type ManagedEnvironment map[string]*string

// ResolveManagedExecutableRequest resolves one executable with the effective environment.
type ResolveManagedExecutableRequest struct {
	Executable string             `json:"executable"`
	Env        ManagedEnvironment `json:"env,omitempty"`
}

// ResolveManagedExecutableResponse contains one validated absolute path.
type ResolveManagedExecutableResponse struct {
	Path string `json:"path"`
}

// CreateManagedProcessRequest starts one exact-argv process.
type CreateManagedProcessRequest struct {
	OperationID          string             `json:"operationId"`
	Argv                 []string           `json:"argv"`
	Cwd                  string             `json:"cwd"`
	Env                  ManagedEnvironment `json:"env,omitempty"`
	Stdin                string             `json:"stdin"`
	StdoutRetentionBytes *int64             `json:"stdoutRetentionBytes,omitempty"`
	StderrRetentionBytes *int64             `json:"stderrRetentionBytes,omitempty"`
	GraceMS              *int64             `json:"graceMs,omitempty"`
}

// TerminateManagedRequest optionally overrides the create-time grace period.
type TerminateManagedRequest struct {
	GraceMS *int64 `json:"graceMs,omitempty"`
}

// ManagedProcessStatus is the public lifecycle view of one managed process.
type ManagedProcessStatus struct {
	ProcessID          string  `json:"processId"`
	PID                *int    `json:"pid,omitempty"`
	State              string  `json:"state"`
	ExitCode           *int    `json:"exitCode"`
	Signal             *string `json:"signal"`
	TopLevelExited     bool    `json:"topLevelExited"`
	TreeEmpty          bool    `json:"treeEmpty"`
	StdinSequence      uint64  `json:"stdinSequence"`
	StdoutOffset       int64   `json:"stdoutOffset"`
	StderrOffset       int64   `json:"stderrOffset"`
	StdoutRetainedFrom int64   `json:"stdoutRetainedFrom"`
	StderrRetainedFrom int64   `json:"stderrRetainedFrom"`
	StdoutSpillPath    *string `json:"stdoutSpillPath"`
	StderrSpillPath    *string `json:"stderrSpillPath"`
}

// CreateManagedTerminalRequest allocates one exact-argv terminal.
type CreateManagedTerminalRequest struct {
	OperationID string             `json:"operationId"`
	Argv        []string           `json:"argv"`
	Cwd         string             `json:"cwd"`
	Env         ManagedEnvironment `json:"env,omitempty"`
	Rows        uint16             `json:"rows"`
	Cols        uint16             `json:"cols"`
	GraceMS     *int64             `json:"graceMs,omitempty"`
}

// ManagedTerminalStatus is the public lifecycle view of one terminal.
type ManagedTerminalStatus struct {
	TerminalID         string  `json:"terminalId"`
	PID                *int    `json:"pid,omitempty"`
	State              string  `json:"state"`
	ExitCode           *int    `json:"exitCode"`
	Signal             *string `json:"signal"`
	TopLevelExited     bool    `json:"topLevelExited"`
	TreeEmpty          bool    `json:"treeEmpty"`
	OutputOffset       int64   `json:"outputOffset"`
	OutputRetainedFrom int64   `json:"outputRetainedFrom"`
	OutputEOF          bool    `json:"outputEof"`
}

// ManagedTerminalForeground reports the active terminal process group.
type ManagedTerminalForeground struct {
	ProcessGroup int  `json:"processGroup"`
	InputWaiting bool `json:"inputWaiting"`
}

// SignalManagedTerminalRequest selects a supported foreground signal.
type SignalManagedTerminalRequest struct {
	Signal string `json:"signal"`
}

// SignalManagedTerminalResponse identifies the process group that received the signal.
type SignalManagedTerminalResponse struct {
	ProcessGroup int `json:"processGroup"`
}

// Managed WebSocket binary frame tags.
const (
	ManagedBinStdin  byte = 0x00
	ManagedBinStdout byte = 0x01
	ManagedBinStderr byte = 0x02
)

// Managed WebSocket error and close codes.
const (
	ManagedWSErrInvalidFrame = "INVALID_FRAME"
	ManagedWSErrRuntime      = "RUNTIME_ERROR"
	ManagedWSCloseTakenOver  = 4001
)
