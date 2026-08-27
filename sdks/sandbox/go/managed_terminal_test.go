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

func managedTerminalStatus() ManagedTerminalStatus {
	pid := 5432
	return ManagedTerminalStatus{
		TerminalID: "term/1",
		PID:        &pid,
		State:      ManagedTerminalRunning,
	}
}

func TestManagedTerminalControlAndDeferredPublication(t *testing.T) {
	type createBody struct {
		OperationID       string                     `json:"operationId"`
		Argv              []string                   `json:"argv"`
		Cwd               string                     `json:"cwd"`
		Env               ManagedTerminalEnvironment `json:"env"`
		Rows              int                        `json:"rows"`
		Cols              int                        `json:"cols"`
		GraceMilliseconds *int64                     `json:"graceMs"`
	}
	type terminateBody struct {
		GraceMilliseconds *int64 `json:"graceMs"`
	}

	createStarted := make(chan createBody, 1)
	releaseCreate := make(chan struct{})
	terminateBodies := make(chan *int64, 2)
	_, client := newExecdServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/terminals":
			var request createBody
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			createStarted <- request
			<-releaseCreate
			jsonResponse(w, http.StatusCreated, managedTerminalStatus())
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/terminals/term%2F1":
			status := managedTerminalStatus()
			status.OutputOffset = 12
			jsonResponse(w, http.StatusOK, status)
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/terminals/term%2F1/foreground":
			jsonResponse(w, http.StatusOK, ManagedTerminalForeground{ProcessGroup: 5432, InputWaiting: true})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/v1/terminals/term%2F1/foreground/signal":
			var request signalManagedTerminalForegroundWireRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			require.Equal(t, ManagedTerminalSignalInterrupt, request.Signal)
			jsonResponse(w, http.StatusOK, signalManagedTerminalForegroundWireResponse{ProcessGroup: 5432})
		case r.Method == http.MethodPost && r.URL.EscapedPath() == "/v1/terminals/term%2F1/terminate":
			var request terminateBody
			if r.ContentLength != 0 {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			}
			terminateBodies <- request.GraceMilliseconds
			status := managedTerminalStatus()
			signal := "SIGKILL"
			status.State = ManagedTerminalQuiescent
			status.Signal = &signal
			status.TopLevelExited = true
			status.TreeEmpty = true
			jsonResponse(w, http.StatusOK, status)
		case r.Method == http.MethodDelete && r.URL.EscapedPath() == "/v1/terminals/term%2F1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	sandbox := &Sandbox{execd: client}

	grace := 3 * time.Second
	langValue := "C.UTF-8"
	handle := sandbox.StartManagedTerminal(context.Background(), CreateManagedTerminalRequest{
		OperationID: "operation-1",
		Argv:        []string{"/usr/bin/bash", "-l"},
		Cwd:         "/workspace",
		Env: ManagedTerminalEnvironment{
			"LANG":    &langValue,
			"REMOVED": nil,
		},
		Rows:  40,
		Cols:  120,
		Grace: &grace,
	})
	request := <-createStarted
	_, published := handle.TerminalID()
	if published {
		t.Fatal("terminal ID should not be published before create completes")
	}
	require.Equal(t, "operation-1", request.OperationID)
	require.Equal(t, []string{"/usr/bin/bash", "-l"}, request.Argv)
	require.Equal(t, "/workspace", request.Cwd)
	require.Equal(t, "C.UTF-8", *request.Env["LANG"])
	if request.Env["REMOVED"] != nil {
		t.Fatal("REMOVED environment value should remain null")
	}
	require.Equal(t, 40, request.Rows)
	require.Equal(t, 120, request.Cols)
	require.Equal(t, int64(3000), *request.GraceMilliseconds)

	close(releaseCreate)
	ready, err := handle.WaitReady(context.Background())
	require.NoError(t, err)
	require.Equal(t, 5432, ready.PID)
	terminalID, published := handle.TerminalID()
	require.True(t, published)
	require.Equal(t, "term/1", terminalID)

	status, err := handle.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(12), status.OutputOffset)
	foreground, err := handle.Foreground(context.Background())
	require.NoError(t, err)
	require.Equal(t, 5432, foreground.ProcessGroup)
	require.True(t, foreground.InputWaiting)
	signaledGroup, err := handle.SignalForeground(context.Background(), ManagedTerminalSignalInterrupt)
	require.NoError(t, err)
	require.Equal(t, 5432, signaledGroup)

	zero := time.Duration(0)
	status, err = handle.Terminate(context.Background(), &TerminateManagedTerminalOptions{Grace: &zero})
	require.NoError(t, err)
	require.Equal(t, ManagedTerminalQuiescent, status.State)
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

func TestManagedTerminalHandleRejectsMissingPublicationFacts(t *testing.T) {
	_, client := newExecdServer(t, func(w http.ResponseWriter, r *http.Request) {
		status := managedTerminalStatus()
		status.PID = nil
		jsonResponse(w, http.StatusCreated, status)
	})
	handle := (&Sandbox{execd: client}).StartManagedTerminal(
		context.Background(),
		CreateManagedTerminalRequest{
			OperationID: "operation-unpublished",
			Argv:        []string{"/bin/sh"},
			Cwd:         "/workspace",
			Rows:        24,
			Cols:        80,
		},
	)

	_, err := handle.WaitReady(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "omitted terminalId or pid")
}
