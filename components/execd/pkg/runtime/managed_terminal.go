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

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrManagedTerminalNotFound          = errors.New("managed terminal not found")
	ErrManagedTerminalInvalidRequest    = errors.New("invalid managed terminal request")
	ErrManagedTerminalOperationConflict = errors.New("managed terminal operation id conflicts with an existing request")
	ErrManagedTerminalOperationDeleted  = errors.New("managed terminal operation was deleted")
	ErrManagedTerminalNotQuiescent      = errors.New("managed terminal is not quiescent")
	ErrManagedTerminalManagerClosed     = errors.New("managed terminal manager is closed")
	ErrManagedTerminalUnsupported       = errors.New("managed terminals are not supported on this platform")
	ErrManagedTerminalClosing           = errors.New("managed terminal is closing")
	ErrManagedTerminalInactive          = errors.New("managed terminal is not active")
	ErrManagedTerminalOutputOffset      = errors.New("invalid managed terminal output offset")
	ErrManagedTerminalSignal            = errors.New("unsupported managed terminal signal")
)

// ManagedTerminalSignal identifies a supported foreground terminal signal.
type ManagedTerminalSignal string

const (
	ManagedTerminalSignalInterrupt ManagedTerminalSignal = "SIGINT"
	ManagedTerminalSignalTerminate ManagedTerminalSignal = "SIGTERM"
	ManagedTerminalSignalKill      ManagedTerminalSignal = "SIGKILL"
	ManagedTerminalSignalStop      ManagedTerminalSignal = "SIGTSTP"
	ManagedTerminalSignalHangup    ManagedTerminalSignal = "SIGHUP"
)

// ManagedTerminalState describes direct-process and terminal-session progress.
type ManagedTerminalState string

const (
	ManagedTerminalRunning   ManagedTerminalState = "running"
	ManagedTerminalExited    ManagedTerminalState = "exited"
	ManagedTerminalQuiescent ManagedTerminalState = "quiescent"
)

// ManagedTerminalRequest contains exact-argv PTY start parameters.
type ManagedTerminalRequest struct {
	OperationID string
	Argv        []string
	Cwd         string
	Env         map[string]*string
	Rows        uint16
	Cols        uint16
	Grace       time.Duration
}

// ManagedTerminalStatus separates the direct outcome from complete session quiescence.
type ManagedTerminalStatus struct {
	TerminalID         string
	PID                int
	State              ManagedTerminalState
	ExitCode           *int
	Signal             *string
	TopLevelExited     bool
	TreeEmpty          bool
	OutputOffset       int64
	OutputRetainedFrom int64
	OutputEOF          bool
}

// ManagedTerminalOutput is retained merged PTY output at an absolute byte offset.
type ManagedTerminalOutput struct {
	Offset     int64
	NextOffset int64
	Data       []byte
	Gap        bool
	EOF        bool
}

// ManagedTerminalForeground reports the terminal's active foreground process group.
type ManagedTerminalForeground struct {
	ProcessGroup int
	InputWaiting bool
}

type managedTerminalOperation struct {
	request  ManagedTerminalRequest
	ready    chan struct{}
	terminal *ManagedTerminal
	err      error
	deleted  bool
}

// ManagedTerminalManager owns all terminal sessions created by one execd instance.
type ManagedTerminalManager struct {
	mu          sync.Mutex
	closed      bool
	operations  map[string]*managedTerminalOperation
	terminals   map[string]*ManagedTerminal
	allocations sync.WaitGroup
}

// NewManagedTerminalManager creates an empty in-memory terminal registry.
func NewManagedTerminalManager() *ManagedTerminalManager {
	return &ManagedTerminalManager{
		operations: make(map[string]*managedTerminalOperation),
		terminals:  make(map[string]*ManagedTerminal),
	}
}

// Create starts one exact-argv terminal or returns an identical operation's result.
func (m *ManagedTerminalManager) Create(request ManagedTerminalRequest) (*ManagedTerminal, bool, error) {
	request = cloneManagedTerminalRequest(request)
	if err := validateManagedTerminalRequest(request); err != nil {
		return nil, false, invalidManagedTerminalRequest(err)
	}

	m.mu.Lock()
	if existing := m.operations[request.OperationID]; existing != nil {
		if existing.deleted {
			m.mu.Unlock()
			return nil, false, ErrManagedTerminalOperationDeleted
		}
		if !reflect.DeepEqual(existing.request, request) {
			m.mu.Unlock()
			return nil, false, ErrManagedTerminalOperationConflict
		}
		ready := existing.ready
		m.mu.Unlock()
		<-ready
		m.mu.Lock()
		terminal, existingErr, deleted := existing.terminal, existing.err, existing.deleted
		m.mu.Unlock()
		if deleted {
			return nil, false, ErrManagedTerminalOperationDeleted
		}
		return terminal, false, existingErr
	}
	if m.closed {
		m.mu.Unlock()
		return nil, false, ErrManagedTerminalManagerClosed
	}
	operation := &managedTerminalOperation{request: request, ready: make(chan struct{})}
	m.operations[request.OperationID] = operation
	m.allocations.Add(1)
	m.mu.Unlock()
	defer m.allocations.Done()

	terminal, err := startManagedTerminal(request)
	m.mu.Lock()
	operation.terminal = terminal
	operation.err = err
	if err != nil {
		delete(m.operations, request.OperationID)
	} else {
		m.terminals[terminal.id] = terminal
	}
	close(operation.ready)
	m.mu.Unlock()
	return terminal, err == nil, err
}

// Get returns a published terminal by opaque ID.
func (m *ManagedTerminalManager) Get(id string) (*ManagedTerminal, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	terminal, ok := m.terminals[id]
	return terminal, ok
}

// Terminate joins TERM-to-KILL cleanup and waits for the complete session to quiesce.
func (m *ManagedTerminalManager) Terminate(ctx context.Context, id string, grace *time.Duration) (ManagedTerminalStatus, error) {
	terminal, ok := m.Get(id)
	if !ok {
		return ManagedTerminalStatus{}, ErrManagedTerminalNotFound
	}
	if grace != nil && *grace < 0 {
		return ManagedTerminalStatus{}, errors.New("managed terminal grace must not be negative")
	}
	terminal.startTermination(grace)
	select {
	case <-ctx.Done():
		return terminal.Status(), ctx.Err()
	case <-terminal.terminateDone:
		return terminal.Status(), terminal.terminationError()
	}
}

// Delete removes a quiescent terminal and its retained output.
func (m *ManagedTerminalManager) Delete(id string) error {
	m.mu.Lock()
	terminal := m.terminals[id]
	if terminal == nil {
		m.mu.Unlock()
		return ErrManagedTerminalNotFound
	}
	status := terminal.Status()
	if status.State != ManagedTerminalQuiescent || !status.OutputEOF {
		m.mu.Unlock()
		return ErrManagedTerminalNotQuiescent
	}
	delete(m.terminals, id)
	operation := m.operations[terminal.operationID]
	operation.deleted = true
	operation.terminal = nil
	operation.request = ManagedTerminalRequest{}
	m.mu.Unlock()
	terminal.beginClosing()
	terminal.close()
	return nil
}

// Shutdown rejects allocations, kills live sessions, and waits for their quiescence.
func (m *ManagedTerminalManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	allocationsDone := make(chan struct{})
	go func() {
		m.allocations.Wait()
		close(allocationsDone)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-allocationsDone:
	}

	m.mu.Lock()
	terminals := make([]*ManagedTerminal, 0, len(m.terminals))
	for _, terminal := range m.terminals {
		terminals = append(terminals, terminal)
	}
	m.mu.Unlock()

	for _, terminal := range terminals {
		terminal.forceKill()
	}
	for _, terminal := range terminals {
		if err := terminal.waitForQuiescence(ctx); err != nil {
			return err
		}
		terminal.beginClosing()
		terminal.close()
	}
	return nil
}

// ManagedTerminal owns one exact-argv process session and its PTY.
type ManagedTerminal struct {
	id           string
	operationID  string
	pid          int
	sessionStart uint64
	grace        time.Duration
	ptmx         *os.File

	mu             sync.Mutex
	topLevelExited bool
	treeEmpty      bool
	exitCode       *int
	signal         *string
	done           chan struct{}
	treeDone       chan struct{}

	outputMu      sync.Mutex
	replay        *replayBuffer
	outputEOF     bool
	outputUpdated chan struct{}
	outputDone    chan struct{}

	operationsMu sync.Mutex
	closing      bool
	operations   sync.WaitGroup

	terminateOnce sync.Once
	terminateDone chan struct{}
	terminateMu   sync.Mutex
	terminateErr  error
	closeOnce     sync.Once
}

// ID returns the opaque terminal identity.
func (t *ManagedTerminal) ID() string { return t.id }

// Done closes when the direct process outcome is available.
func (t *ManagedTerminal) Done() <-chan struct{} { return t.done }

// Status returns a consistent terminal lifecycle snapshot.
func (t *ManagedTerminal) Status() ManagedTerminalStatus {
	t.mu.Lock()
	state := ManagedTerminalRunning
	if t.topLevelExited {
		state = ManagedTerminalExited
		if t.treeEmpty {
			state = ManagedTerminalQuiescent
		}
	}
	status := ManagedTerminalStatus{
		TerminalID:     t.id,
		PID:            t.pid,
		State:          state,
		ExitCode:       cloneInt(t.exitCode),
		Signal:         cloneString(t.signal),
		TopLevelExited: t.topLevelExited,
		TreeEmpty:      t.treeEmpty,
	}
	t.mu.Unlock()

	t.outputMu.Lock()
	status.OutputOffset = t.replay.Total()
	status.OutputRetainedFrom = max(int64(0), status.OutputOffset-int64(t.replay.size))
	status.OutputEOF = t.outputEOF
	t.outputMu.Unlock()
	return status
}

// WriteInput writes raw bytes to the terminal master.
func (t *ManagedTerminal) WriteInput(data []byte) (int, error) {
	if !t.beginOperation() {
		return 0, ErrManagedTerminalClosing
	}
	defer t.operations.Done()
	return t.ptmx.Write(data)
}

// Resize changes the PTY dimensions.
func (t *ManagedTerminal) Resize(rows, cols uint16) error {
	if rows == 0 || cols == 0 {
		return errors.New("managed terminal rows and columns must be positive")
	}
	if !t.beginOperation() {
		return ErrManagedTerminalClosing
	}
	defer t.operations.Done()
	return resizeManagedTerminal(t.ptmx, rows, cols)
}

// Foreground reports the current foreground process group.
func (t *ManagedTerminal) Foreground() (ManagedTerminalForeground, error) {
	if !t.beginOperation() {
		return ManagedTerminalForeground{}, ErrManagedTerminalClosing
	}
	defer t.operations.Done()
	if t.sessionQuiescent() {
		return ManagedTerminalForeground{}, ErrManagedTerminalInactive
	}
	group, err := foregroundManagedTerminal(t.ptmx, t.pid, t.sessionStart)
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) || t.directExited() {
			return ManagedTerminalForeground{}, ErrManagedTerminalInactive
		}
		return ManagedTerminalForeground{}, err
	}
	return ManagedTerminalForeground{ProcessGroup: group, InputWaiting: false}, nil
}

// SignalForeground sends one supported signal to the active foreground group.
func (t *ManagedTerminal) SignalForeground(signal ManagedTerminalSignal) error {
	if !t.beginOperation() {
		return ErrManagedTerminalClosing
	}
	defer t.operations.Done()
	if t.sessionQuiescent() {
		return ErrManagedTerminalInactive
	}
	group, err := foregroundManagedTerminal(t.ptmx, t.pid, t.sessionStart)
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) || t.directExited() {
			return ErrManagedTerminalInactive
		}
		return err
	}
	err = signalManagedTerminalForeground(t.pid, t.sessionStart, group, signal)
	if errors.Is(err, os.ErrProcessDone) {
		return ErrManagedTerminalInactive
	}
	return err
}

func (t *ManagedTerminal) directExited() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.topLevelExited
}

func (t *ManagedTerminal) sessionQuiescent() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.topLevelExited && t.treeEmpty
}

// ReadOutput waits for merged PTY bytes, an offset gap, or output EOF.
func (t *ManagedTerminal) ReadOutput(ctx context.Context, offset int64) (ManagedTerminalOutput, error) {
	if offset < 0 {
		return ManagedTerminalOutput{}, ErrManagedTerminalOutputOffset
	}
	for {
		t.outputMu.Lock()
		total := t.replay.Total()
		if offset > total {
			t.outputMu.Unlock()
			return ManagedTerminalOutput{}, fmt.Errorf("%w: got=%d current=%d", ErrManagedTerminalOutputOffset, offset, total)
		}
		retainedFrom := max(int64(0), total-int64(t.replay.size))
		actual := max(offset, retainedFrom)
		data, actual := t.replay.ReadFrom(actual)
		if len(data) > managedTerminalOutputChunkSize {
			data = data[:managedTerminalOutputChunkSize]
		}
		gap := actual != offset
		if len(data) > 0 || gap || t.outputEOF {
			nextOffset := actual + int64(len(data))
			result := ManagedTerminalOutput{
				Offset:     actual,
				NextOffset: nextOffset,
				Data:       data,
				Gap:        gap,
				EOF:        t.outputEOF && nextOffset == total,
			}
			t.outputMu.Unlock()
			return result, nil
		}
		updated := t.outputUpdated
		t.outputMu.Unlock()
		select {
		case <-ctx.Done():
			return ManagedTerminalOutput{}, ctx.Err()
		case <-updated:
		}
	}
}

const managedTerminalOutputChunkSize = 32 * 1024

func startManagedTerminal(request ManagedTerminalRequest) (*ManagedTerminal, error) {
	env, err := managedProcessEnvironment(request.Env)
	if err != nil {
		return nil, invalidManagedTerminalRequest(err)
	}
	cwd, err := managedProcessWorkingDir(request.Cwd, true)
	if err != nil {
		return nil, invalidManagedTerminalRequest(err)
	}
	executable, err := resolveManagedExecutable(request.Argv[0], cwd, env)
	if err != nil {
		return nil, invalidManagedTerminalRequest(err)
	}

	cmd := exec.Command(executable)
	cmd.Path = executable
	cmd.Args = append([]string(nil), request.Argv...)
	cmd.Dir = cwd
	cmd.Env = env
	ptmx, err := startManagedTerminalPTY(cmd, request.Rows, request.Cols)
	if err != nil {
		return nil, fmt.Errorf("start managed terminal: %w", err)
	}
	sessionStart, err := managedProcessStartIdentity(cmd.Process.Pid)
	if err != nil {
		_ = forceManagedProcessGroup(cmd.Process.Pid, 0)
		_ = cmd.Wait()
		_ = ptmx.Close()
		return nil, fmt.Errorf("record managed terminal identity: %w", err)
	}

	terminal := &ManagedTerminal{
		id:            uuid.NewString(),
		operationID:   request.OperationID,
		pid:           cmd.Process.Pid,
		sessionStart:  sessionStart,
		grace:         request.Grace,
		ptmx:          ptmx,
		done:          make(chan struct{}),
		treeDone:      make(chan struct{}),
		replay:        newReplayBuffer(),
		outputUpdated: make(chan struct{}),
		outputDone:    make(chan struct{}),
		terminateDone: make(chan struct{}),
	}
	go terminal.pumpOutput()
	go terminal.wait(cmd)
	go terminal.observeSession()
	return terminal, nil
}

func (t *ManagedTerminal) pumpOutput() {
	buffer := make([]byte, 32*1024)
	for {
		n, err := t.ptmx.Read(buffer)
		if n > 0 {
			t.outputMu.Lock()
			t.replay.write(buffer[:n])
			t.notifyOutputLocked()
			t.outputMu.Unlock()
		}
		if err != nil {
			t.outputMu.Lock()
			t.outputEOF = true
			t.notifyOutputLocked()
			t.outputMu.Unlock()
			close(t.outputDone)
			return
		}
	}
}

func (t *ManagedTerminal) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	exitCode, signalName := managedProcessExitOutcome(cmd.ProcessState, err)
	t.mu.Lock()
	t.exitCode = exitCode
	t.signal = signalName
	t.topLevelExited = true
	t.mu.Unlock()
	close(t.done)
}

func (t *ManagedTerminal) observeSession() {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		live, err := managedTerminalSessionLive(t.pid, t.sessionStart)
		if err == nil && !live {
			t.markTreeEmpty()
			return
		}
		<-ticker.C
	}
}

func (t *ManagedTerminal) beginOperation() bool {
	t.operationsMu.Lock()
	defer t.operationsMu.Unlock()
	if t.closing {
		return false
	}
	t.operations.Add(1)
	return true
}

func (t *ManagedTerminal) beginClosing() {
	t.stopOperations()
	t.operations.Wait()
}

func (t *ManagedTerminal) stopOperations() {
	t.operationsMu.Lock()
	t.closing = true
	t.operationsMu.Unlock()
}

func (t *ManagedTerminal) startTermination(graceOverride *time.Duration) {
	t.terminateOnce.Do(func() {
		grace := t.grace
		if graceOverride != nil {
			grace = *graceOverride
		}
		go t.runTermination(grace, t.signalSession)
	})
}

func (t *ManagedTerminal) runTermination(grace time.Duration, signalSession func(ManagedTerminalSignal) error) {
	defer close(t.terminateDone)
	t.stopOperations()
	if grace > 0 && signalSession(ManagedTerminalSignalTerminate) == nil {
		timer := time.NewTimer(grace)
		select {
		case <-t.treeDone:
			timer.Stop()
		case <-timer.C:
		}
	}
	select {
	case <-t.treeDone:
	default:
		if err := signalSession(ManagedTerminalSignalKill); err != nil {
			t.setTerminationError(err)
		}
		<-t.treeDone
	}
	t.operations.Wait()
	t.waitForTerminalDrain()
}

func (t *ManagedTerminal) signalSession(signal ManagedTerminalSignal) error {
	live, err := managedTerminalSessionLive(t.pid, t.sessionStart)
	if err != nil {
		return err
	}
	if !live {
		t.markTreeEmpty()
		return nil
	}
	if err := signalManagedTerminalSession(t.pid, t.sessionStart, signal); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			t.markTreeEmpty()
			return nil
		}
		return err
	}
	return nil
}

func (t *ManagedTerminal) forceKill() {
	t.stopOperations()
	go func() {
		if err := t.signalSession(ManagedTerminalSignalKill); err != nil {
			t.setTerminationError(err)
		}
	}()
}

func (t *ManagedTerminal) waitForTerminalDrain() {
	<-t.done
	<-t.outputDone
}

func (t *ManagedTerminal) waitForQuiescence(ctx context.Context) error {
	for _, done := range []<-chan struct{}{t.treeDone, t.done, t.outputDone} {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
	return t.terminationError()
}

func (t *ManagedTerminal) markTreeEmpty() {
	t.mu.Lock()
	if t.treeEmpty {
		t.mu.Unlock()
		return
	}
	t.treeEmpty = true
	close(t.treeDone)
	t.mu.Unlock()
}

func (t *ManagedTerminal) setTerminationError(err error) {
	t.terminateMu.Lock()
	defer t.terminateMu.Unlock()
	if t.terminateErr == nil {
		t.terminateErr = err
	}
}

func (t *ManagedTerminal) terminationError() error {
	t.terminateMu.Lock()
	defer t.terminateMu.Unlock()
	return t.terminateErr
}

func (t *ManagedTerminal) notifyOutputLocked() {
	close(t.outputUpdated)
	t.outputUpdated = make(chan struct{})
}

func (t *ManagedTerminal) close() {
	t.closeOnce.Do(func() { _ = t.ptmx.Close() })
}

func validateManagedTerminalRequest(request ManagedTerminalRequest) error {
	if request.Rows == 0 || request.Cols == 0 {
		return errors.New("managed terminal rows and columns must be positive")
	}
	processRequest := ManagedProcessRequest{
		OperationID: request.OperationID,
		Argv:        request.Argv,
		Cwd:         request.Cwd,
		Env:         request.Env,
		Grace:       request.Grace,
	}
	if err := validateManagedProcessRequest(processRequest); err != nil {
		return fmt.Errorf("invalid managed terminal request: %w", err)
	}
	return nil
}

func invalidManagedTerminalRequest(err error) error {
	return fmt.Errorf("%w: %v", ErrManagedTerminalInvalidRequest, err)
}

func cloneManagedTerminalRequest(request ManagedTerminalRequest) ManagedTerminalRequest {
	request.Argv = append([]string(nil), request.Argv...)
	if request.Env != nil {
		env := request.Env
		request.Env = make(map[string]*string, len(env))
		for key, value := range env {
			request.Env[key] = cloneString(value)
		}
	}
	return request
}
