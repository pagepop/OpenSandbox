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

package main

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alibaba/opensandbox/execd/pkg/runtime"
)

func TestServeExecdGracefulShutdown(t *testing.T) {
	processManager := runtime.NewManagedProcessManager()
	terminalManager := runtime.NewManagedTerminalManager()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Handler: http.NotFoundHandler()}
	t.Cleanup(func() { _ = server.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- serveExecd(ctx, server, listener, processManager, terminalManager, 2*time.Second)
	}()
	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		response, requestErr := client.Get("http://" + listener.Addr().String())
		if requestErr != nil {
			return false
		}
		_ = response.Body.Close()
		return true
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("graceful shutdown did not complete")
	}
	assertManagedRuntimeManagersClosed(t, processManager, terminalManager)
}

func TestServeExecdUnexpectedExitCleansUp(t *testing.T) {
	processManager := runtime.NewManagedProcessManager()
	terminalManager := runtime.NewManagedTerminalManager()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	server := &http.Server{Handler: http.NotFoundHandler()}

	err = serveExecd(context.Background(), server, listener, processManager, terminalManager, 2*time.Second)
	require.ErrorContains(t, err, "execd server stopped unexpectedly")
	assertManagedRuntimeManagersClosed(t, processManager, terminalManager)
}

func TestServeExecdShutdownDeadlineForcesConnectionsClosed(t *testing.T) {
	processManager := runtime.NewManagedProcessManager()
	terminalManager := runtime.NewManagedTerminalManager()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	defer close(releaseRequest)
	server := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(requestStarted)
		<-releaseRequest
	})}
	t.Cleanup(func() { _ = server.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- serveExecd(ctx, server, listener, processManager, terminalManager, 50*time.Millisecond)
	}()
	clientDone := make(chan error, 1)
	go func() {
		response, requestErr := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + listener.Addr().String())
		if response != nil {
			_ = response.Body.Close()
		}
		clientDone <- requestErr
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking request did not start")
	}

	cancel()
	select {
	case err := <-result:
		require.ErrorContains(t, err, "execd shutdown deadline")
	case <-time.After(time.Second):
		t.Fatal("shutdown did not honor its deadline")
	}
	select {
	case err := <-clientDone:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("shutdown deadline did not force-close the active connection")
	}
	assertManagedRuntimeManagersClosed(t, processManager, terminalManager)
}

func assertManagedRuntimeManagersClosed(
	t *testing.T,
	processManager *runtime.ManagedProcessManager,
	terminalManager *runtime.ManagedTerminalManager,
) {
	t.Helper()
	cwd := t.TempDir()
	missing := filepath.Join(cwd, "missing")
	_, _, err := processManager.Create(runtime.ManagedProcessRequest{
		OperationID: "after-shutdown",
		Argv:        []string{missing},
		Cwd:         cwd,
	})
	require.ErrorIs(t, err, runtime.ErrManagedProcessManagerClosed)
	_, _, err = terminalManager.Create(runtime.ManagedTerminalRequest{
		OperationID: "after-shutdown",
		Argv:        []string{missing},
		Cwd:         cwd,
		Rows:        1,
		Cols:        1,
	})
	require.ErrorIs(t, err, runtime.ErrManagedTerminalManagerClosed)
}
