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

//go:build !windows
// +build !windows

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const managedProcessHelperEnv = "GO_WANT_MANAGED_PROCESS_HELPER"

func TestManagedProcessHelper(t *testing.T) {
	if os.Getenv(managedProcessHelperEnv) != "1" {
		return
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(90)
	}
	args := os.Args[separator+1:]
	switch args[0] {
	case "argv-env":
		payload := struct {
			Argv []string          `json:"argv"`
			Env  map[string]string `json:"env"`
		}{
			Argv: os.Args,
			Env: map[string]string{
				"HOST_API_KEY":   os.Getenv("HOST_API_KEY"),
				"EXPLICIT_TOKEN": os.Getenv("EXPLICIT_TOKEN"),
				"REMOVE_ME":      os.Getenv("REMOVE_ME"),
			},
		}
		_ = json.NewEncoder(os.Stdout).Encode(payload)
		_, _ = os.Stderr.Write([]byte{0, '\r', '\n', 0xff})
		os.Exit(0)
	case "copy":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		os.Exit(0)
	case "output":
		_, _ = os.Stdout.Write([]byte("0123456789"))
		_, _ = os.Stderr.Write([]byte("wxyz"))
		os.Exit(0)
	case "exit":
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		_, _ = os.Stdout.Write([]byte("ready"))
		select {}
	case "spawn-child":
		executable, err := os.Executable()
		if err != nil {
			os.Exit(91)
		}
		child := exec.Command(executable, "-test.run=^TestManagedProcessHelper$", "--", "sleep")
		child.Env = os.Environ()
		if err := child.Start(); err != nil {
			os.Exit(92)
		}
		_, _ = fmt.Fprintf(os.Stdout, "%d", child.Process.Pid)
		os.Exit(0)
	case "sleep":
		select {}
	default:
		os.Exit(93)
	}
}

func TestManagedProcessExactArgvEnvironmentAndOutcome(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, path := managedProcessHelperCommand(t)
	t.Setenv("HOST_API_KEY", "host-secret")
	t.Setenv("REMOVE_ME", "remove-this")
	explicitToken := "caller-value"
	process, created, err := manager.Create(ManagedProcessRequest{
		OperationID: "exact-argv",
		Argv: append(command,
			"argv-env", "", "space value", `quote"value`, "$not-expanded", "世界"),
		Cwd: path,
		Env: map[string]*string{
			"PATH":                  stringPointer(path),
			managedProcessHelperEnv: stringPointer("1"),
			"EXPLICIT_TOKEN":        &explicitToken,
			"REMOVE_ME":             nil,
		},
		StdoutRetentionBytes: 32 * 1024,
		StderrRetentionBytes: 32 * 1024,
		Grace:                time.Second,
	})
	require.NoError(t, err)
	require.True(t, created)
	waitManagedProcessDone(t, process)

	stdout := readManagedProcessOutput(t, process, ManagedProcessStdout)
	stderr := readManagedProcessOutput(t, process, ManagedProcessStderr)
	var payload struct {
		Argv []string          `json:"argv"`
		Env  map[string]string `json:"env"`
	}
	require.NoError(t, json.Unmarshal(stdout.data, &payload))
	require.Equal(t, command[0], payload.Argv[0])
	require.Equal(t, []string{"argv-env", "", "space value", `quote"value`, "$not-expanded", "世界"}, payload.Argv[len(payload.Argv)-6:])
	require.Empty(t, payload.Env["HOST_API_KEY"])
	require.Equal(t, explicitToken, payload.Env["EXPLICIT_TOKEN"])
	require.Empty(t, payload.Env["REMOVE_ME"])
	require.Equal(t, []byte{0, '\r', '\n', 0xff}, stderr.data)

	status := process.Status()
	require.Equal(t, ManagedProcessQuiescent, status.State)
	require.Equal(t, 0, *status.ExitCode)
	require.Nil(t, status.Signal)
	require.NotNil(t, status.StdoutSpillPath)
	require.NotNil(t, status.StderrSpillPath)

	resolved, err := manager.ResolveExecutable(ManagedProcessResolveRequest{
		Executable: command[0],
		Env:        map[string]*string{"PATH": stringPointer(path)},
	})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(path, command[0]), resolved)
	_, err = manager.ResolveExecutable(ManagedProcessResolveRequest{Executable: "./relative"})
	require.Error(t, err)
}

func TestManagedProcessEnvironmentScrubsCredentialNames(t *testing.T) {
	for _, name := range []string{
		"AWS_ACCESS_KEY_ID",
		"TENCENTCLOUD_SECRET_ID",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"AZURE_STORAGE_KEY",
	} {
		require.True(t, scrubManagedProcessEnv(name), name)
	}
	for _, name := range []string{"HOME", "LANG", "PATH"} {
		require.False(t, scrubManagedProcessEnv(name), name)
	}
}

func TestManagedProcessIdempotencyAndSequencedStdin(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, cwd := managedProcessHelperCommand(t)
	request := managedProcessRequest(command, cwd, "stdin", "copy")
	process, created, err := manager.Create(request)
	require.NoError(t, err)
	require.True(t, created)

	replayed, created, err := manager.Create(request)
	require.NoError(t, err)
	require.False(t, created)
	require.Same(t, process, replayed)
	conflict := request
	conflict.Argv = append(append([]string(nil), request.Argv...), "different")
	_, _, err = manager.Create(conflict)
	require.ErrorIs(t, err, ErrManagedProcessOperationConflict)

	first, err := process.AttachStdin(0)
	require.NoError(t, err)
	acked, err := first.WriteStdin(1, []byte("hello"))
	require.NoError(t, err)
	require.Equal(t, uint64(1), acked)
	acked, err = first.WriteStdin(1, []byte("-duplicate"))
	require.NoError(t, err)
	require.Equal(t, uint64(1), acked)

	second, err := process.AttachStdin(0)
	require.NoError(t, err)
	select {
	case <-first.Done():
	default:
		t.Fatal("replaced attachment was not evicted")
	}
	_, err = first.WriteStdin(2, []byte("ignored"))
	require.ErrorIs(t, err, ErrManagedProcessStdinNotOwner)
	_, err = second.WriteStdin(3, []byte("gap"))
	require.ErrorIs(t, err, ErrManagedProcessStdinSequence)
	acked, err = second.WriteStdin(2, []byte(" world"))
	require.NoError(t, err)
	require.Equal(t, uint64(2), acked)
	_, err = second.CloseStdin(2)
	require.ErrorIs(t, err, ErrManagedProcessStdinSequence)
	acked, err = second.CloseStdin(3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), acked)
	acked, err = second.CloseStdin(3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), acked)
	_, err = second.WriteStdin(3, []byte("after-eof"))
	require.ErrorIs(t, err, io.ErrClosedPipe)

	waitManagedProcessDone(t, process)
	stdout := readManagedProcessOutput(t, process, ManagedProcessStdout)
	require.Equal(t, []byte("hello world"), stdout.data)
	require.Equal(t, uint64(3), process.Status().StdinSequence)

	require.NoError(t, manager.Delete(process.ID()))
	_, found := manager.Get(process.ID())
	require.False(t, found)
	require.Nil(t, manager.operations[request.OperationID].process)
	require.Empty(t, manager.operations[request.OperationID].request.OperationID)
	_, _, err = manager.Create(request)
	require.ErrorIs(t, err, ErrManagedProcessOperationDeleted)
	_, _, err = manager.Create(conflict)
	require.ErrorIs(t, err, ErrManagedProcessOperationDeleted)
}

func TestManagedProcessStdinTakeoverWaitsForInFlightWrite(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, cwd := managedProcessHelperCommand(t)
	process, _, err := manager.Create(managedProcessRequest(command, cwd, "blocked-stdin", "sleep"))
	require.NoError(t, err)
	first, err := process.AttachStdin(0)
	require.NoError(t, err)

	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := first.WriteStdin(1, bytes.Repeat([]byte("x"), 1<<20))
		writeDone <- writeErr
	}()
	select {
	case err := <-writeDone:
		t.Fatalf("stdin write completed before filling the unread pipe: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	type attachResult struct {
		attachment *ManagedProcessAttachment
		err        error
	}
	attached := make(chan attachResult, 1)
	go func() {
		attachment, attachErr := process.AttachStdin(0)
		attached <- attachResult{attachment: attachment, err: attachErr}
	}()
	select {
	case <-attached:
		t.Fatal("stdin takeover passed an in-flight write")
	case <-time.After(50 * time.Millisecond):
	}

	zero := time.Duration(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = manager.Terminate(ctx, process.ID(), &zero)
	require.NoError(t, err)
	require.Error(t, <-writeDone)
	result := <-attached
	require.NoError(t, result.err)
	second := result.attachment
	defer second.Release()
	select {
	case <-first.Done():
	default:
		t.Fatal("stdin takeover did not evict the prior attachment")
	}
}

func TestManagedProcessOutputRetentionAndGap(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, cwd := managedProcessHelperCommand(t)
	request := managedProcessRequest(command, cwd, "retention", "output")
	request.StdoutRetentionBytes = 4
	request.StderrRetentionBytes = 4
	process, _, err := manager.Create(request)
	require.NoError(t, err)
	waitManagedProcessDone(t, process)

	stdout := readManagedProcessOutput(t, process, ManagedProcessStdout)
	require.True(t, stdout.gap)
	require.Equal(t, int64(6), stdout.offset)
	require.Equal(t, []byte("6789"), stdout.data)
	stderr := readManagedProcessOutput(t, process, ManagedProcessStderr)
	require.False(t, stderr.gap)
	require.Equal(t, []byte("wxyz"), stderr.data)

	status := process.Status()
	require.Equal(t, int64(10), status.StdoutOffset)
	require.Equal(t, int64(6), status.StdoutRetainedFrom)
	require.Nil(t, status.StdoutSpillPath)
	require.NotNil(t, status.StderrSpillPath)
	require.Equal(t, filepath.Join(process.dir, "stderr"), *status.StderrSpillPath)
}

func TestManagedProcessZeroGracePreservesOutput(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, cwd := managedProcessHelperCommand(t)
	request := managedProcessRequest(command, cwd, "zero-grace-output", "output")
	request.Grace = 0
	process, _, err := manager.Create(request)
	require.NoError(t, err)
	waitManagedProcessDone(t, process)

	stdout := readManagedProcessOutput(t, process, ManagedProcessStdout)
	stderr := readManagedProcessOutput(t, process, ManagedProcessStderr)
	require.Equal(t, []byte("0123456789"), stdout.data)
	require.Equal(t, []byte("wxyz"), stderr.data)
}

func TestManagedProcessOutputReportsZeroRetentionGapBeforeEOF(t *testing.T) {
	output, err := newManagedProcessOutput(filepath.Join(t.TempDir(), "output"), 0)
	require.NoError(t, err)
	t.Cleanup(output.remove)
	_, err = output.Write([]byte("lost"))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := output.read(ctx, ManagedProcessStdout, 0)
	require.NoError(t, err)
	require.True(t, result.Gap)
	require.Equal(t, int64(4), result.Offset)
	require.Equal(t, int64(4), result.NextOffset)
	require.Empty(t, result.Data)
	require.False(t, result.EOF)
}

func TestManagedProcessTerminationAndSurvivingGroup(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, cwd := managedProcessHelperCommand(t)
	t.Run("term escalates to kill", func(t *testing.T) {
		request := managedProcessRequest(command, cwd, "terminate", "ignore-term")
		request.Grace = 20 * time.Millisecond
		process, _, err := manager.Create(request)
		require.NoError(t, err)
		readyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ready, err := process.ReadOutput(readyCtx, ManagedProcessStdout, 0)
		require.NoError(t, err)
		require.Equal(t, []byte("ready"), ready.Data)

		status, err := manager.Terminate(context.Background(), process.ID(), nil)
		require.NoError(t, err)
		require.Equal(t, ManagedProcessQuiescent, status.State)
		require.Nil(t, status.ExitCode)
		require.Equal(t, "SIGKILL", *status.Signal)
		repeated, err := manager.Terminate(context.Background(), process.ID(), nil)
		require.NoError(t, err)
		require.Equal(t, status.Signal, repeated.Signal)
	})

	t.Run("direct exit is separate from group quiescence", func(t *testing.T) {
		process, _, err := manager.Create(managedProcessRequest(command, cwd, "survivor", "spawn-child"))
		require.NoError(t, err)
		waitDirectManagedProcessExit(t, process)
		status := process.Status()
		require.True(t, status.TopLevelExited)
		require.False(t, status.TreeEmpty)
		require.Equal(t, ManagedProcessExited, status.State)
		require.Equal(t, 0, *status.ExitCode)

		zero := time.Duration(0)
		status, err = manager.Terminate(context.Background(), process.ID(), &zero)
		require.NoError(t, err)
		require.Equal(t, ManagedProcessQuiescent, status.State)
		require.Equal(t, 0, *status.ExitCode)
		require.Nil(t, status.Signal)
	})
}

func TestManagedProcessSpawnFailureAndShutdown(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, cwd := managedProcessHelperCommand(t)
	request := managedProcessRequest(command, cwd, "retry-operation", "sleep")
	bad := request
	bad.Argv = []string{"/does/not/exist"}
	_, _, err := manager.Create(bad)
	require.ErrorIs(t, err, ErrManagedProcessInvalidRequest)

	process, created, err := manager.Create(request)
	require.NoError(t, err)
	require.True(t, created)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, manager.Shutdown(ctx))
	waitDirectManagedProcessExit(t, process)
	require.Equal(t, ManagedProcessQuiescent, process.Status().State)
	_, _, err = manager.Create(managedProcessRequest(command, cwd, "after-shutdown", "exit", "0"))
	require.ErrorIs(t, err, ErrManagedProcessManagerClosed)
}

func TestManagedProcessShutdownWaitsForAllocation(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, cwd := managedProcessHelperCommand(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	manager.beforeStart = func() {
		close(entered)
		<-release
	}
	type allocationResult struct {
		process *ManagedProcess
		err     error
	}
	createResult := make(chan allocationResult, 1)
	go func() {
		process, _, err := manager.Create(managedProcessRequest(command, cwd, "allocating", "sleep"))
		createResult <- allocationResult{process: process, err: err}
	}()
	<-entered

	shutdownResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownResult <- manager.Shutdown(ctx)
	}()
	select {
	case err := <-shutdownResult:
		t.Fatalf("shutdown returned before allocation completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	created := <-createResult
	require.NoError(t, created.err)
	require.NoError(t, <-shutdownResult)
	require.Equal(t, ManagedProcessQuiescent, created.process.Status().State)
}

func TestManagedProcessConcurrentCreateStartsOnce(t *testing.T) {
	manager := newTestManagedProcessManager(t)
	command, cwd := managedProcessHelperCommand(t)
	request := managedProcessRequest(command, cwd, "concurrent", "sleep")
	entered := make(chan struct{})
	release := make(chan struct{})
	manager.beforeStart = func() {
		close(entered)
		<-release
	}
	type result struct {
		process *ManagedProcess
		created bool
		err     error
	}
	results := make(chan result, 2)
	create := func() {
		process, created, err := manager.Create(request)
		results <- result{process: process, created: created, err: err}
	}
	go create()
	<-entered
	go create()
	select {
	case result := <-results:
		t.Fatalf("duplicate create returned before publication: %+v", result)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	first := <-results
	second := <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Same(t, first.process, second.process)
	require.NotEqual(t, first.created, second.created)
}

func managedProcessHelperCommand(t *testing.T) ([]string, string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	name := "managed-helper"
	path := filepath.Join(dir, name)
	require.NoError(t, os.Symlink(executable, path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NotZero(t, info.Mode().Perm()&0o111)
	return []string{name, "-test.run=^TestManagedProcessHelper$", "--"}, dir
}

func managedProcessRequest(command []string, cwd, operationID string, args ...string) ManagedProcessRequest {
	return ManagedProcessRequest{
		OperationID:          operationID,
		Argv:                 append(append([]string(nil), command...), args...),
		Cwd:                  cwd,
		Env:                  map[string]*string{"PATH": stringPointer(cwd), managedProcessHelperEnv: stringPointer("1")},
		StdoutRetentionBytes: 32 * 1024,
		StderrRetentionBytes: 32 * 1024,
		Grace:                time.Second,
	}
}

type collectedManagedOutput struct {
	data   []byte
	offset int64
	gap    bool
}

func readManagedProcessOutput(t *testing.T, process *ManagedProcess, stream ManagedProcessStream) collectedManagedOutput {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result collectedManagedOutput
	var offset int64
	for {
		output, err := process.ReadOutput(ctx, stream, offset)
		require.NoError(t, err)
		if len(result.data) == 0 {
			result.offset = output.Offset
		}
		result.gap = result.gap || output.Gap
		result.data = append(result.data, output.Data...)
		offset = output.NextOffset
		if output.EOF {
			return result
		}
	}
}

func waitManagedProcessDone(t *testing.T, process *ManagedProcess) {
	t.Helper()
	waitDirectManagedProcessExit(t, process)
	select {
	case <-process.treeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("managed process group did not become quiescent")
	}
}

func waitDirectManagedProcessExit(t *testing.T, process *ManagedProcess) {
	t.Helper()
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("managed direct process did not exit")
	}
}

func stringPointer(value string) *string { return &value }

func newTestManagedProcessManager(t *testing.T) *ManagedProcessManager {
	t.Helper()
	manager := NewManagedProcessManager()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
		manager.mu.Lock()
		ids := make([]string, 0, len(manager.processes))
		for id := range manager.processes {
			ids = append(ids, id)
		}
		manager.mu.Unlock()
		for _, id := range ids {
			_ = manager.Delete(id)
		}
	})
	return manager
}

func TestManagedProcessOutputReadCancellation(t *testing.T) {
	output, err := newManagedProcessOutput(filepath.Join(t.TempDir(), "output"), 8)
	require.NoError(t, err)
	t.Cleanup(output.remove)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = output.read(ctx, ManagedProcessStdout, 0)
	require.ErrorIs(t, err, context.Canceled)
	_, err = output.read(context.Background(), ManagedProcessStdout, 1)
	require.ErrorIs(t, err, ErrManagedProcessOutputOffset)
}

func TestManagedProcessOutputReadsBoundedChunks(t *testing.T) {
	output, err := newManagedProcessOutput(filepath.Join(t.TempDir(), "output"), 2*managedProcessOutputChunkSize)
	require.NoError(t, err)
	t.Cleanup(output.remove)
	data := bytes.Repeat([]byte("x"), int(managedProcessOutputChunkSize+7))
	_, err = output.Write(data)
	require.NoError(t, err)
	output.finish(true)

	first, err := output.read(context.Background(), ManagedProcessStdout, 0)
	require.NoError(t, err)
	require.Len(t, first.Data, int(managedProcessOutputChunkSize))
	require.False(t, first.EOF)
	second, err := output.read(context.Background(), ManagedProcessStdout, first.NextOffset)
	require.NoError(t, err)
	require.Equal(t, data[managedProcessOutputChunkSize:], second.Data)
	require.True(t, second.EOF)
}
