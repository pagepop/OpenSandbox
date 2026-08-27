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
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func managedProcessStatus() ManagedProcessStatus {
	pid := 4321
	return ManagedProcessStatus{
		ProcessID: "proc/1",
		PID:       &pid,
		State:     ManagedProcessRunning,
	}
}

func TestManagedProcessControlAndDeferredPublication(t *testing.T) {
	type createBody struct {
		OperationID          string                    `json:"operationId"`
		Argv                 []string                  `json:"argv"`
		Cwd                  string                    `json:"cwd"`
		Env                  ManagedProcessEnvironment `json:"env"`
		Stdin                ManagedProcessStdinMode   `json:"stdin"`
		StdoutRetentionBytes *int64                    `json:"stdoutRetentionBytes"`
		StderrRetentionBytes *int64                    `json:"stderrRetentionBytes"`
		GraceMilliseconds    *int64                    `json:"graceMs"`
	}
	type terminateBody struct {
		GraceMilliseconds *int64 `json:"graceMs"`
	}

	createStarted := make(chan createBody, 1)
	releaseCreate := make(chan struct{})
	terminateBodies := make(chan *int64, 2)
	_, client := newExecdServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/processes/resolve-executable":
			var request ResolveExecutableRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			require.Equal(t, "node", request.Executable)
			require.Equal(t, "/usr/bin", *request.Env["PATH"])
			if request.Env["REMOVED"] != nil {
				t.Fatal("REMOVED environment value should remain null")
			}
			jsonResponse(w, http.StatusOK, ResolveExecutableResponse{Path: "/usr/bin/node"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/processes":
			var request createBody
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			createStarted <- request
			<-releaseCreate
			jsonResponse(w, http.StatusCreated, managedProcessStatus())
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/processes/proc%2F1":
			status := managedProcessStatus()
			status.StdoutOffset = 12
			jsonResponse(w, http.StatusOK, status)
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/v1/processes/proc%2F1/terminate":
			var request terminateBody
			if r.ContentLength != 0 {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			}
			terminateBodies <- request.GraceMilliseconds
			status := managedProcessStatus()
			signal := "SIGKILL"
			status.State = ManagedProcessQuiescent
			status.Signal = &signal
			status.TopLevelExited = true
			status.TreeEmpty = true
			jsonResponse(w, http.StatusOK, status)
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/v1/processes/proc%2F1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	sandbox := &Sandbox{execd: client}

	pathValue := "/usr/bin"
	resolved, err := sandbox.ResolveExecutable(context.Background(), ResolveExecutableRequest{
		Executable: "node",
		Env: ManagedProcessEnvironment{
			"PATH":    &pathValue,
			"REMOVED": nil,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/node", resolved.Path)

	stdoutRetention := int64(1024)
	stderrRetention := int64(2048)
	grace := 3 * time.Second
	langValue := "C.UTF-8"
	handle := sandbox.StartManagedProcess(context.Background(), CreateManagedProcessRequest{
		OperationID: "operation-1",
		Argv:        []string{"/usr/bin/node", "script with spaces.js", ""},
		Cwd:         "/workspace",
		Env: ManagedProcessEnvironment{
			"LANG":    &langValue,
			"REMOVED": nil,
		},
		Stdin:                ManagedProcessStdinPipe,
		StdoutRetentionBytes: &stdoutRetention,
		StderrRetentionBytes: &stderrRetention,
		Grace:                &grace,
	})
	request := <-createStarted
	_, published := handle.ProcessID()
	if published {
		t.Fatal("process ID should not be published before create completes")
	}
	_, published = handle.PID()
	if published {
		t.Fatal("PID should not be published before create completes")
	}

	require.Equal(t, "operation-1", request.OperationID)
	require.Equal(t, []string{"/usr/bin/node", "script with spaces.js", ""}, request.Argv)
	require.Equal(t, "/workspace", request.Cwd)
	require.Equal(t, "C.UTF-8", *request.Env["LANG"])
	if request.Env["REMOVED"] != nil {
		t.Fatal("REMOVED environment value should remain null")
	}
	require.Equal(t, ManagedProcessStdinPipe, request.Stdin)
	require.Equal(t, int64(1024), *request.StdoutRetentionBytes)
	require.Equal(t, int64(2048), *request.StderrRetentionBytes)
	require.Equal(t, int64(3000), *request.GraceMilliseconds)

	close(releaseCreate)
	ready, err := handle.WaitReady(context.Background())
	require.NoError(t, err)
	require.Equal(t, 4321, ready.PID)
	processID, published := handle.ProcessID()
	require.True(t, published)
	require.Equal(t, "proc/1", processID)
	pid, published := handle.PID()
	require.True(t, published)
	require.Equal(t, 4321, pid)

	status, err := handle.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(12), status.StdoutOffset)
	zero := time.Duration(0)
	status, err = handle.Terminate(context.Background(), &TerminateManagedProcessOptions{Grace: &zero})
	require.NoError(t, err)
	require.Equal(t, ManagedProcessQuiescent, status.State)
	require.Equal(t, "SIGKILL", *status.Signal)
	if status.ExitCode != nil {
		t.Fatal("signal termination should not publish an exit code")
	}
	graceMilliseconds := <-terminateBodies
	require.NotNil(t, graceMilliseconds)
	require.Equal(t, int64(0), *graceMilliseconds)
	_, err = handle.Terminate(context.Background(), nil)
	require.NoError(t, err)
	if graceMilliseconds = <-terminateBodies; graceMilliseconds != nil {
		t.Fatal("nil termination options should omit the request body")
	}
	require.NoError(t, handle.Delete(context.Background()))
}

func TestManagedProcessHandlePublishesCreateFailure(t *testing.T) {
	_, client := newExecdServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "spawn failed", http.StatusInternalServerError)
	})
	sandbox := &Sandbox{execd: client}
	handle := sandbox.StartManagedProcess(context.Background(), CreateManagedProcessRequest{
		OperationID: "operation-failed",
		Argv:        []string{"/missing"},
		Cwd:         "/workspace",
		Stdin:       ManagedProcessStdinPipe,
	})

	_, err := handle.WaitReady(context.Background())
	require.Error(t, err)
	_, published := handle.ProcessID()
	if published {
		t.Fatal("failed create should not publish a process ID")
	}
	_, published = handle.PID()
	if published {
		t.Fatal("failed create should not publish a PID")
	}
}

func TestManagedProcessHandleRejectsMissingPublicationFacts(t *testing.T) {
	_, client := newExecdServer(t, func(w http.ResponseWriter, r *http.Request) {
		status := managedProcessStatus()
		status.PID = nil
		jsonResponse(w, http.StatusCreated, status)
	})
	sandbox := &Sandbox{execd: client}
	handle := sandbox.StartManagedProcess(context.Background(), CreateManagedProcessRequest{
		OperationID: "operation-unpublished",
		Argv:        []string{"/bin/true"},
		Cwd:         "/workspace",
		Stdin:       ManagedProcessStdinPipe,
	})

	_, err := handle.WaitReady(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "omitted processId or pid")
}
