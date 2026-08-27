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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const managedTerminalHelperEnv = "GO_WANT_MANAGED_TERMINAL_HELPER"

func TestManagedTerminalHelper(t *testing.T) {
	if os.Getenv(managedTerminalHelperEnv) != "1" {
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
	case "facts":
		writeManagedTerminalFacts(args[1:])
	case "size-on-input":
		buffer := make([]byte, 1)
		_, _ = os.Stdin.Read(buffer)
		writeManagedTerminalFacts(nil)
	case "bytes":
		_, _ = os.Stdout.Write([]byte{0, 0x1b, '[', 'A', 0xff})
		_, _ = os.Stderr.Write([]byte("stderr"))
	case "exit":
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		_, _ = os.Stdout.Write([]byte("ready"))
		select {}
	case "sleep":
		select {}
	case "spawn-child":
		executable, err := os.Executable()
		if err != nil {
			os.Exit(91)
		}
		child := exec.Command(executable, "-test.run=^TestManagedTerminalHelper$", "--", "sleep")
		child.Env = os.Environ()
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := child.Start(); err != nil {
			os.Exit(92)
		}
		_, _ = fmt.Fprintf(os.Stdout, "%d", child.Process.Pid)
	default:
		os.Exit(93)
	}
	os.Exit(0)
}

func writeManagedTerminalFacts(args []string) {
	workingDir, _ := os.Getwd()
	size, err := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		os.Exit(94)
	}
	payload := struct {
		Args []string `json:"args"`
		Cwd  string   `json:"cwd"`
		Env  string   `json:"env"`
		Rows uint16   `json:"rows"`
		Cols uint16   `json:"cols"`
	}{
		Args: args,
		Cwd:  workingDir,
		Env:  os.Getenv("TERMINAL_VALUE"),
		Rows: size.Row,
		Cols: size.Col,
	}
	_ = json.NewEncoder(os.Stdout).Encode(payload)
}

func TestManagedTerminalExactRequestOutputAndIdempotency(t *testing.T) {
	manager := NewManagedTerminalManager()
	command, cwd := managedTerminalHelperCommand(t)
	value := "explicit value"
	request := managedTerminalRequest(command, cwd, "exact", "facts", "", "space value", `quote"value`, "$literal", "世界")
	request.Env["TERMINAL_VALUE"] = &value
	request.Rows = 31
	request.Cols = 97
	invalid := request
	invalid.Argv = []string{"/does/not/exist"}
	_, _, err := manager.Create(invalid)
	require.ErrorIs(t, err, ErrManagedTerminalInvalidRequest)

	terminal, created, err := manager.Create(request)
	require.NoError(t, err)
	require.True(t, created)
	replayed, created, err := manager.Create(request)
	require.NoError(t, err)
	require.False(t, created)
	require.Same(t, terminal, replayed)
	conflict := request
	conflict.Cols++
	_, _, err = manager.Create(conflict)
	require.ErrorIs(t, err, ErrManagedTerminalOperationConflict)

	waitManagedTerminalQuiescent(t, terminal)
	output := readManagedTerminalOutput(t, terminal)
	var facts struct {
		Args []string `json:"args"`
		Cwd  string   `json:"cwd"`
		Env  string   `json:"env"`
		Rows uint16   `json:"rows"`
		Cols uint16   `json:"cols"`
	}
	require.NoError(t, json.Unmarshal(output, &facts))
	require.Equal(t, []string{"", "space value", `quote"value`, "$literal", "世界"}, facts.Args)
	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	require.Equal(t, resolvedCwd, facts.Cwd)
	require.Equal(t, value, facts.Env)
	require.Equal(t, uint16(31), facts.Rows)
	require.Equal(t, uint16(97), facts.Cols)

	status := terminal.Status()
	require.Equal(t, ManagedTerminalQuiescent, status.State)
	require.Equal(t, 0, *status.ExitCode)
	require.Nil(t, status.Signal)
	require.True(t, status.OutputEOF)
	_, err = terminal.Foreground()
	require.ErrorIs(t, err, ErrManagedTerminalInactive)
	require.ErrorIs(t, terminal.SignalForeground(ManagedTerminalSignalInterrupt), ErrManagedTerminalInactive)
	require.NoError(t, manager.Delete(terminal.ID()))
	_, found := manager.Get(terminal.ID())
	require.False(t, found)
	require.Nil(t, manager.operations[request.OperationID].terminal)
	require.Empty(t, manager.operations[request.OperationID].request.OperationID)
	_, _, err = manager.Create(request)
	require.ErrorIs(t, err, ErrManagedTerminalOperationDeleted)
	_, _, err = manager.Create(conflict)
	require.ErrorIs(t, err, ErrManagedTerminalOperationDeleted)
}

func TestManagedTerminalRawMergedOutputAndOffsets(t *testing.T) {
	manager := NewManagedTerminalManager()
	command, cwd := managedTerminalHelperCommand(t)
	terminal, _, err := manager.Create(managedTerminalRequest(command, cwd, "raw-output", "bytes"))
	require.NoError(t, err)
	waitManagedTerminalQuiescent(t, terminal)

	output := readManagedTerminalOutput(t, terminal)
	require.Equal(t, []byte{0, 0x1b, '[', 'A', 0xff, 's', 't', 'd', 'e', 'r', 'r'}, output)
	tail, err := terminal.ReadOutput(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), tail.Offset)
	require.Equal(t, output[2:], tail.Data)
	require.True(t, tail.EOF)
	_, err = terminal.ReadOutput(context.Background(), int64(len(output)+1))
	require.ErrorIs(t, err, ErrManagedTerminalOutputOffset)

	replay := &replayBuffer{buf: make([]byte, 4), size: 4}
	replay.write([]byte("abcdef"))
	fixture := &ManagedTerminal{replay: replay, outputEOF: true, outputUpdated: make(chan struct{})}
	gapped, err := fixture.ReadOutput(context.Background(), 0)
	require.NoError(t, err)
	require.True(t, gapped.Gap)
	require.Equal(t, int64(2), gapped.Offset)
	require.Equal(t, []byte("cdef"), gapped.Data)
}

func TestManagedTerminalResizeForegroundAndSignals(t *testing.T) {
	manager := NewManagedTerminalManager()
	command, cwd := managedTerminalHelperCommand(t)
	terminal, _, err := manager.Create(managedTerminalRequest(command, cwd, "resize", "size-on-input"))
	require.NoError(t, err)

	foreground, err := terminal.Foreground()
	require.NoError(t, err)
	require.Equal(t, terminal.pid, foreground.ProcessGroup)
	require.False(t, foreground.InputWaiting)
	require.NoError(t, terminal.Resize(42, 118))
	_, err = terminal.WriteInput([]byte("\n"))
	require.NoError(t, err)
	waitManagedTerminalQuiescent(t, terminal)
	output := string(readManagedTerminalOutput(t, terminal))
	start := strings.IndexByte(output, '{')
	end := strings.LastIndexByte(output, '}')
	require.NotEqual(t, -1, start)
	require.GreaterOrEqual(t, end, start)
	var size struct {
		Rows uint16 `json:"rows"`
		Cols uint16 `json:"cols"`
	}
	require.NoError(t, json.Unmarshal([]byte(output[start:end+1]), &size))
	require.Equal(t, uint16(42), size.Rows)
	require.Equal(t, uint16(118), size.Cols)

	signalTerminal, _, err := manager.Create(managedTerminalRequest(command, cwd, "foreground-signal", "sleep"))
	require.NoError(t, err)
	require.ErrorIs(t, manager.Delete(signalTerminal.ID()), ErrManagedTerminalNotQuiescent)
	require.NoError(t, signalTerminal.SignalForeground(ManagedTerminalSignalInterrupt))
	waitManagedTerminalQuiescent(t, signalTerminal)
	status := signalTerminal.Status()
	require.Nil(t, status.ExitCode)
	require.Equal(t, "SIGINT", *status.Signal)

	for signal, expected := range map[ManagedTerminalSignal]syscall.Signal{
		ManagedTerminalSignalInterrupt: syscall.SIGINT,
		ManagedTerminalSignalTerminate: syscall.SIGTERM,
		ManagedTerminalSignalKill:      syscall.SIGKILL,
		ManagedTerminalSignalStop:      syscall.SIGTSTP,
		ManagedTerminalSignalHangup:    syscall.SIGHUP,
	} {
		actual, err := managedTerminalSignalValue(signal)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	}
	_, err = managedTerminalSignalValue("SIGUSR1")
	require.ErrorIs(t, err, ErrManagedTerminalSignal)
}

func TestManagedTerminalTerminationAndShutdown(t *testing.T) {
	command, cwd := managedTerminalHelperCommand(t)
	t.Run("TERM escalates and repeated calls join", func(t *testing.T) {
		manager := NewManagedTerminalManager()
		request := managedTerminalRequest(command, cwd, "terminate", "ignore-term")
		request.Grace = 20 * time.Millisecond
		terminal, _, err := manager.Create(request)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ready, err := terminal.ReadOutput(ctx, 0)
		require.NoError(t, err)
		require.Equal(t, []byte("ready"), ready.Data)
		writeStarted := make(chan struct{})
		writeDone := make(chan error, 1)
		go func() {
			close(writeStarted)
			_, err := terminal.WriteInput(bytes.Repeat([]byte("x"), 8<<20))
			writeDone <- err
		}()
		<-writeStarted
		select {
		case <-writeDone:
			t.Fatal("large PTY write unexpectedly completed before termination")
		case <-time.After(50 * time.Millisecond):
		}

		status, err := manager.Terminate(ctx, terminal.ID(), nil)
		require.NoError(t, err)
		require.Equal(t, ManagedTerminalQuiescent, status.State)
		require.Equal(t, "SIGKILL", *status.Signal)
		repeated, err := manager.Terminate(ctx, terminal.ID(), nil)
		require.NoError(t, err)
		require.Equal(t, status.Signal, repeated.Signal)
		select {
		case <-writeDone:
		case <-time.After(5 * time.Second):
			t.Fatal("blocked PTY write did not unblock after termination")
		}
		_, err = terminal.WriteInput([]byte("closed"))
		require.ErrorIs(t, err, ErrManagedTerminalClosing)
	})

	t.Run("shutdown awaits terminal cleanup", func(t *testing.T) {
		manager := NewManagedTerminalManager()
		terminal, _, err := manager.Create(managedTerminalRequest(command, cwd, "shutdown", "sleep"))
		require.NoError(t, err)
		writeDone := make(chan error, 1)
		go func() {
			_, err := terminal.WriteInput(bytes.Repeat([]byte("x"), 8<<20))
			writeDone <- err
		}()
		select {
		case <-writeDone:
			t.Fatal("large PTY write unexpectedly completed before shutdown")
		case <-time.After(50 * time.Millisecond):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, manager.Shutdown(ctx))
		require.Equal(t, ManagedTerminalQuiescent, terminal.Status().State)
		select {
		case <-writeDone:
		case <-time.After(5 * time.Second):
			t.Fatal("blocked PTY write did not unblock during shutdown")
		}
		_, _, err = manager.Create(managedTerminalRequest(command, cwd, "after-shutdown", "exit", "0"))
		require.ErrorIs(t, err, ErrManagedTerminalManagerClosed)
	})

	if goruntime.GOOS == "linux" {
		t.Run("direct exit remains non-quiescent while another session group lives", func(t *testing.T) {
			manager := NewManagedTerminalManager()
			terminal, _, err := manager.Create(managedTerminalRequest(command, cwd, "session-child", "spawn-child"))
			require.NoError(t, err)
			waitManagedTerminalDirectExit(t, terminal)
			status := terminal.Status()
			require.True(t, status.TopLevelExited)
			require.False(t, status.TreeEmpty)
			require.Equal(t, ManagedTerminalExited, status.State)
			zero := time.Duration(0)
			status, err = manager.Terminate(context.Background(), terminal.ID(), &zero)
			require.NoError(t, err)
			require.Equal(t, ManagedTerminalQuiescent, status.State)
		})
	}
}

func TestManagedTerminalKillFailureWaitsForQuiescence(t *testing.T) {
	treeDone := make(chan struct{})
	done := make(chan struct{})
	close(done)
	outputDone := make(chan struct{})
	close(outputDone)
	terminal := &ManagedTerminal{
		treeDone:      treeDone,
		done:          done,
		outputDone:    outputDone,
		terminateDone: make(chan struct{}),
	}
	wantErr := errors.New("kill failed")
	killAttempted := make(chan struct{})
	var signals []ManagedTerminalSignal
	go terminal.runTermination(0, func(signal ManagedTerminalSignal) error {
		signals = append(signals, signal)
		if signal == ManagedTerminalSignalKill {
			close(killAttempted)
			return wantErr
		}
		return nil
	})
	<-killAttempted
	require.Equal(t, []ManagedTerminalSignal{ManagedTerminalSignalKill}, signals)
	require.ErrorIs(t, terminal.terminationError(), wantErr)
	select {
	case <-terminal.terminateDone:
		t.Fatal("termination completed before session quiescence")
	default:
	}
	close(treeDone)
	select {
	case <-terminal.terminateDone:
	case <-time.After(time.Second):
		t.Fatal("termination did not complete after session quiescence")
	}
}

func managedTerminalHelperCommand(t *testing.T) ([]string, string) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	name := "managed-terminal-helper"
	require.NoError(t, os.Symlink(executable, filepath.Join(dir, name)))
	return []string{name, "-test.run=^TestManagedTerminalHelper$", "--"}, dir
}

func managedTerminalRequest(command []string, cwd, operationID string, args ...string) ManagedTerminalRequest {
	return ManagedTerminalRequest{
		OperationID: operationID,
		Argv:        append(append([]string(nil), command...), args...),
		Cwd:         cwd,
		Env: map[string]*string{
			"PATH":                   stringPointer(cwd),
			managedTerminalHelperEnv: stringPointer("1"),
		},
		Rows:  24,
		Cols:  80,
		Grace: time.Second,
	}
}

func readManagedTerminalOutput(t *testing.T, terminal *ManagedTerminal) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var data []byte
	var offset int64
	for {
		output, err := terminal.ReadOutput(ctx, offset)
		require.NoError(t, err)
		data = append(data, output.Data...)
		offset = output.NextOffset
		if output.EOF {
			return data
		}
	}
}

func waitManagedTerminalQuiescent(t *testing.T, terminal *ManagedTerminal) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, terminal.waitForQuiescence(ctx))
}

func waitManagedTerminalDirectExit(t *testing.T, terminal *ManagedTerminal) {
	t.Helper()
	select {
	case <-terminal.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("managed terminal direct process did not exit")
	}
}
