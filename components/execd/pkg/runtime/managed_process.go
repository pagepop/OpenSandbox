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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrManagedProcessNotFound          = errors.New("managed process not found")
	ErrManagedProcessInvalidRequest    = errors.New("invalid managed process request")
	ErrManagedProcessOperationConflict = errors.New("managed process operation id conflicts with an existing request")
	ErrManagedProcessOperationDeleted  = errors.New("managed process operation was deleted")
	ErrManagedProcessNotQuiescent      = errors.New("managed process is not quiescent")
	ErrManagedProcessManagerClosed     = errors.New("managed process manager is closed")
	ErrManagedProcessUnsupported       = errors.New("managed processes are not supported on this platform")
	ErrManagedProcessStdinSequence     = errors.New("invalid managed process stdin sequence")
	ErrManagedProcessStdinNotOwner     = errors.New("managed process stdin attachment is no longer active")
	ErrManagedProcessOutputOffset      = errors.New("invalid managed process output offset")
	ErrManagedProcessStream            = errors.New("invalid managed process stream")
)

// ManagedProcessStream identifies one ordinary process output stream.
type ManagedProcessStream string

const (
	ManagedProcessStdout ManagedProcessStream = "stdout"
	ManagedProcessStderr ManagedProcessStream = "stderr"
)

type managedProcessSignal uint8

const (
	managedProcessSignalTerm managedProcessSignal = iota + 1
	managedProcessSignalKill
)

// ManagedProcessState describes publication and exit progress.
type ManagedProcessState string

const (
	ManagedProcessRunning   ManagedProcessState = "running"
	ManagedProcessExited    ManagedProcessState = "exited"
	ManagedProcessQuiescent ManagedProcessState = "quiescent"
)

// ManagedProcessResolveRequest uses the same environment rules as process creation.
type ManagedProcessResolveRequest struct {
	Executable string
	Env        map[string]*string
}

// ManagedProcessRequest contains the sandbox-side process start parameters.
type ManagedProcessRequest struct {
	OperationID          string
	Argv                 []string
	Cwd                  string
	Env                  map[string]*string
	StdoutRetentionBytes int64
	StderrRetentionBytes int64
	Grace                time.Duration
}

// ManagedProcessStatus separates the direct process outcome from group quiescence.
type ManagedProcessStatus struct {
	ProcessID          string
	PID                int
	State              ManagedProcessState
	ExitCode           *int
	Signal             *string
	TopLevelExited     bool
	TreeEmpty          bool
	StdinSequence      uint64
	StdoutOffset       int64
	StderrOffset       int64
	StdoutRetainedFrom int64
	StderrRetainedFrom int64
	StdoutSpillPath    *string
	StderrSpillPath    *string
}

// ManagedProcessOutput is one retained output chunk starting at an absolute byte offset.
type ManagedProcessOutput struct {
	Stream     ManagedProcessStream
	Offset     int64
	NextOffset int64
	Data       []byte
	Gap        bool
	EOF        bool
	CleanEOF   bool
}

type managedProcessOperation struct {
	request ManagedProcessRequest
	ready   chan struct{}
	process *ManagedProcess
	err     error
	deleted bool
}

// ManagedProcessManager owns all managed processes created by one execd instance.
type ManagedProcessManager struct {
	mu          sync.Mutex
	closed      bool
	operations  map[string]*managedProcessOperation
	processes   map[string]*ManagedProcess
	allocations sync.WaitGroup
	beforeStart func()
}

// NewManagedProcessManager creates an empty in-memory process registry.
func NewManagedProcessManager() *ManagedProcessManager {
	return &ManagedProcessManager{
		operations: make(map[string]*managedProcessOperation),
		processes:  make(map[string]*ManagedProcess),
	}
}

// ResolveExecutable returns one executable absolute path using managed-process environment rules.
func (m *ManagedProcessManager) ResolveExecutable(request ManagedProcessResolveRequest) (string, error) {
	env, err := managedProcessEnvironment(request.Env)
	if err != nil {
		return "", err
	}
	cwd, err := managedProcessWorkingDir("", false)
	if err != nil {
		return "", err
	}
	return resolveManagedExecutable(request.Executable, cwd, env)
}

// Create starts one exact-argv process or returns the prior result for an identical operation ID.
func (m *ManagedProcessManager) Create(request ManagedProcessRequest) (*ManagedProcess, bool, error) {
	request = cloneManagedProcessRequest(request)
	if err := validateManagedProcessRequest(request); err != nil {
		return nil, false, invalidManagedProcessRequest(err)
	}

	m.mu.Lock()
	if existing := m.operations[request.OperationID]; existing != nil {
		if existing.deleted {
			m.mu.Unlock()
			return nil, false, ErrManagedProcessOperationDeleted
		}
		if !reflect.DeepEqual(existing.request, request) {
			m.mu.Unlock()
			return nil, false, ErrManagedProcessOperationConflict
		}
		ready := existing.ready
		m.mu.Unlock()
		<-ready
		m.mu.Lock()
		process, existingErr, deleted := existing.process, existing.err, existing.deleted
		m.mu.Unlock()
		if deleted {
			return nil, false, ErrManagedProcessOperationDeleted
		}
		return process, false, existingErr
	}
	if m.closed {
		m.mu.Unlock()
		return nil, false, ErrManagedProcessManagerClosed
	}
	operation := &managedProcessOperation{request: request, ready: make(chan struct{})}
	m.operations[request.OperationID] = operation
	m.allocations.Add(1)
	m.mu.Unlock()
	defer m.allocations.Done()

	if m.beforeStart != nil {
		m.beforeStart()
	}
	process, err := startManagedProcess(request)
	m.mu.Lock()
	operation.process = process
	operation.err = err
	if err != nil {
		delete(m.operations, request.OperationID)
	} else {
		m.processes[process.id] = process
	}
	close(operation.ready)
	m.mu.Unlock()
	return process, err == nil, err
}

// Get returns a published process by opaque ID.
func (m *ManagedProcessManager) Get(id string) (*ManagedProcess, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	process, ok := m.processes[id]
	return process, ok
}

// Terminate joins the process's TERM-to-KILL attempt and waits for group quiescence.
func (m *ManagedProcessManager) Terminate(ctx context.Context, id string, grace *time.Duration) (ManagedProcessStatus, error) {
	process, ok := m.Get(id)
	if !ok {
		return ManagedProcessStatus{}, ErrManagedProcessNotFound
	}
	if grace != nil && *grace < 0 {
		return ManagedProcessStatus{}, errors.New("managed process grace must not be negative")
	}
	process.startTermination(grace)
	select {
	case <-ctx.Done():
		return process.Status(), ctx.Err()
	case <-process.terminateDone:
		return process.Status(), process.terminationError()
	}
}

// Delete removes a quiescent process and its retained output.
func (m *ManagedProcessManager) Delete(id string) error {
	m.mu.Lock()
	process := m.processes[id]
	if process == nil {
		m.mu.Unlock()
		return ErrManagedProcessNotFound
	}
	status := process.Status()
	if status.State != ManagedProcessQuiescent {
		m.mu.Unlock()
		return ErrManagedProcessNotQuiescent
	}
	delete(m.processes, id)
	operation := m.operations[process.operationID]
	operation.deleted = true
	operation.process = nil
	operation.request = ManagedProcessRequest{}
	m.mu.Unlock()

	process.remove()
	return nil
}

// Shutdown rejects new allocations, force-kills every live group, and waits for quiescence.
func (m *ManagedProcessManager) Shutdown(ctx context.Context) error {
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
	processes := make([]*ManagedProcess, 0, len(m.processes))
	for _, process := range m.processes {
		processes = append(processes, process)
	}
	m.mu.Unlock()

	for _, process := range processes {
		process.forceKill()
	}
	for _, process := range processes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-process.treeDone:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-process.done:
		}
	}
	return nil
}

// ManagedProcess owns one published exact-argv process and its retained I/O.
type ManagedProcess struct {
	id          string
	operationID string
	pid         int
	groupStart  uint64
	grace       time.Duration
	dir         string

	mu             sync.Mutex
	topLevelExited bool
	treeEmpty      bool
	exitCode       *int
	signal         *string
	done           chan struct{}
	treeDone       chan struct{}

	stdinWriteMu    sync.Mutex
	stdinMu         sync.Mutex
	stdin           *os.File
	stdinClosed     bool
	stdinSequence   uint64
	stdinAttachment *ManagedProcessAttachment

	stdout       *managedProcessOutput
	stderr       *managedProcessOutput
	stdoutReader *os.File
	stderrReader *os.File

	terminateOnce sync.Once
	terminateDone chan struct{}
	terminateMu   sync.Mutex
	terminateErr  error
}

// ID returns the opaque process identity.
func (p *ManagedProcess) ID() string { return p.id }

// Done closes when the direct process has been waited and its outcome is available.
func (p *ManagedProcess) Done() <-chan struct{} { return p.done }

// Status returns a consistent process lifecycle snapshot.
func (p *ManagedProcess) Status() ManagedProcessStatus {
	p.mu.Lock()
	state := ManagedProcessRunning
	if p.topLevelExited {
		state = ManagedProcessExited
		if p.treeEmpty {
			state = ManagedProcessQuiescent
		}
	}
	status := ManagedProcessStatus{
		ProcessID:      p.id,
		PID:            p.pid,
		State:          state,
		ExitCode:       cloneInt(p.exitCode),
		Signal:         cloneString(p.signal),
		TopLevelExited: p.topLevelExited,
		TreeEmpty:      p.treeEmpty,
	}
	p.mu.Unlock()

	p.stdinMu.Lock()
	status.StdinSequence = p.stdinSequence
	p.stdinMu.Unlock()
	status.StdoutOffset, status.StdoutRetainedFrom, status.StdoutSpillPath = p.stdout.status()
	status.StderrOffset, status.StderrRetainedFrom, status.StderrSpillPath = p.stderr.status()
	return status
}

// AttachStdin gives a new attachment exclusive stdin ownership and evicts the prior owner.
func (p *ManagedProcess) AttachStdin(lastAck uint64) (*ManagedProcessAttachment, error) {
	p.stdinWriteMu.Lock()
	defer p.stdinWriteMu.Unlock()

	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if lastAck > p.stdinSequence {
		return nil, fmt.Errorf("%w: client=%d server=%d", ErrManagedProcessStdinSequence, lastAck, p.stdinSequence)
	}
	if p.stdinAttachment != nil {
		p.stdinAttachment.releaseLocked()
	}
	attachment := &ManagedProcessAttachment{
		process: p,
		done:    make(chan struct{}),
	}
	p.stdinAttachment = attachment
	return attachment, nil
}

// ReadOutput waits for retained bytes, an offset gap, or stream EOF.
func (p *ManagedProcess) ReadOutput(ctx context.Context, stream ManagedProcessStream, offset int64) (ManagedProcessOutput, error) {
	if offset < 0 {
		return ManagedProcessOutput{}, ErrManagedProcessOutputOffset
	}
	switch stream {
	case ManagedProcessStdout:
		return p.stdout.read(ctx, stream, offset)
	case ManagedProcessStderr:
		return p.stderr.read(ctx, stream, offset)
	default:
		return ManagedProcessOutput{}, ErrManagedProcessStream
	}
}

// ManagedProcessAttachment is the current exclusive writer for process stdin.
type ManagedProcessAttachment struct {
	process  *ManagedProcess
	done     chan struct{}
	released bool
}

// Done closes when this attachment is replaced or released.
func (a *ManagedProcessAttachment) Done() <-chan struct{} { return a.done }

// StdinSequence returns the last input sequence durably accepted by execd.
func (a *ManagedProcessAttachment) StdinSequence() uint64 {
	a.process.stdinMu.Lock()
	defer a.process.stdinMu.Unlock()
	return a.process.stdinSequence
}

// WriteStdin writes one strictly ordered input frame and returns the acknowledged sequence.
func (a *ManagedProcessAttachment) WriteStdin(sequence uint64, data []byte) (uint64, error) {
	p := a.process
	p.stdinWriteMu.Lock()
	defer p.stdinWriteMu.Unlock()

	p.stdinMu.Lock()
	if !a.activeLocked() {
		acked := p.stdinSequence
		p.stdinMu.Unlock()
		return acked, ErrManagedProcessStdinNotOwner
	}
	if p.stdinClosed {
		acked := p.stdinSequence
		p.stdinMu.Unlock()
		return acked, io.ErrClosedPipe
	}
	if sequence <= p.stdinSequence {
		acked := p.stdinSequence
		p.stdinMu.Unlock()
		return acked, nil
	}
	if sequence != p.stdinSequence+1 {
		acked := p.stdinSequence
		p.stdinMu.Unlock()
		return acked, fmt.Errorf("%w: got=%d want=%d", ErrManagedProcessStdinSequence, sequence, acked+1)
	}
	p.stdinMu.Unlock()

	if len(data) > 0 {
		if _, err := io.Copy(p.stdin, bytes.NewReader(data)); err != nil {
			p.stdinMu.Lock()
			acked := p.stdinSequence
			p.stdinMu.Unlock()
			return acked, err
		}
	}
	p.stdinMu.Lock()
	p.stdinSequence = sequence
	p.stdinMu.Unlock()
	return sequence, nil
}

// CloseStdin acknowledges one ordered EOF frame and closes the process input pipe.
func (a *ManagedProcessAttachment) CloseStdin(sequence uint64) (uint64, error) {
	p := a.process
	p.stdinWriteMu.Lock()
	defer p.stdinWriteMu.Unlock()

	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if !a.activeLocked() {
		return p.stdinSequence, ErrManagedProcessStdinNotOwner
	}
	if p.stdinClosed {
		if sequence == p.stdinSequence {
			return p.stdinSequence, nil
		}
		return p.stdinSequence, fmt.Errorf("%w: got=%d want=%d", ErrManagedProcessStdinSequence, sequence, p.stdinSequence)
	}
	if sequence != p.stdinSequence+1 {
		return p.stdinSequence, fmt.Errorf("%w: got=%d want=%d", ErrManagedProcessStdinSequence, sequence, p.stdinSequence+1)
	}
	p.stdinClosed = true
	err := p.stdin.Close()
	if err != nil {
		return p.stdinSequence, err
	}
	p.stdinSequence = sequence
	return p.stdinSequence, nil
}

// Release relinquishes stdin ownership without closing process stdin.
func (a *ManagedProcessAttachment) Release() {
	p := a.process
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if p.stdinAttachment == a {
		p.stdinAttachment = nil
	}
	a.releaseLocked()
}

func (a *ManagedProcessAttachment) activeLocked() bool {
	return !a.released && a.process.stdinAttachment == a
}

func (a *ManagedProcessAttachment) releaseLocked() {
	if a.released {
		return
	}
	a.released = true
	close(a.done)
}

type managedProcessOutput struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	limit    int64
	total    int64
	eof      bool
	cleanEOF bool
	removed  bool
	updated  chan struct{}
	done     chan struct{}
}

const managedProcessOutputChunkSize int64 = 32 << 10

func newManagedProcessOutput(path string, limit int64) (*managedProcessOutput, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &managedProcessOutput{
		file:    file,
		path:    path,
		limit:   limit,
		updated: make(chan struct{}),
		done:    make(chan struct{}),
	}, nil
}

func (o *managedProcessOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.eof || o.removed {
		return 0, io.ErrClosedPipe
	}
	length := len(data)
	if length == 0 {
		return 0, nil
	}
	if o.limit > 0 {
		absoluteStart := o.total
		retained := data
		if int64(len(retained)) > o.limit {
			skip := int64(len(retained)) - o.limit
			absoluteStart += skip
			retained = retained[skip:]
		}
		position := absoluteStart % o.limit
		first := min(int64(len(retained)), o.limit-position)
		if _, err := o.file.WriteAt(retained[:first], position); err != nil {
			return 0, err
		}
		if first < int64(len(retained)) {
			if _, err := o.file.WriteAt(retained[first:], 0); err != nil {
				return 0, err
			}
		}
	}
	o.total += int64(length)
	o.notifyLocked()
	return length, nil
}

func (o *managedProcessOutput) finish(clean bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.eof {
		return
	}
	if clean && o.total <= o.limit {
		clean = o.file.Sync() == nil
	}
	o.eof = true
	o.cleanEOF = clean
	close(o.done)
	o.notifyLocked()
}

func (o *managedProcessOutput) read(ctx context.Context, stream ManagedProcessStream, offset int64) (ManagedProcessOutput, error) {
	for {
		o.mu.Lock()
		if o.removed {
			o.mu.Unlock()
			return ManagedProcessOutput{}, ErrManagedProcessNotFound
		}
		if offset > o.total {
			o.mu.Unlock()
			return ManagedProcessOutput{}, fmt.Errorf("%w: got=%d current=%d", ErrManagedProcessOutputOffset, offset, o.total)
		}
		retainedFrom := max(int64(0), o.total-o.limit)
		actual := offset
		gap := false
		if actual < retainedFrom {
			actual = retainedFrom
			gap = true
		}
		if gap || actual < o.total || o.eof {
			end := min(o.total, actual+managedProcessOutputChunkSize)
			data, err := o.readRangeLocked(actual, end)
			eof := o.eof && end == o.total
			result := ManagedProcessOutput{
				Stream:     stream,
				Offset:     actual,
				NextOffset: end,
				Data:       data,
				Gap:        gap,
				EOF:        eof,
				CleanEOF:   eof && o.cleanEOF,
			}
			o.mu.Unlock()
			return result, err
		}
		updated := o.updated
		o.mu.Unlock()
		select {
		case <-ctx.Done():
			return ManagedProcessOutput{}, ctx.Err()
		case <-updated:
		}
	}
}

func (o *managedProcessOutput) readRangeLocked(start, end int64) ([]byte, error) {
	length := end - start
	if length == 0 {
		return nil, nil
	}
	data := make([]byte, int(length))
	position := start % o.limit
	first := min(length, o.limit-position)
	if _, err := o.file.ReadAt(data[:first], position); err != nil {
		return nil, err
	}
	if first < length {
		if _, err := o.file.ReadAt(data[first:], 0); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (o *managedProcessOutput) status() (total, retainedFrom int64, spillPath *string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.removed {
		return 0, 0, nil
	}
	retainedFrom = max(int64(0), o.total-o.limit)
	if o.eof && o.cleanEOF && o.total <= o.limit {
		path := o.path
		spillPath = &path
	}
	return o.total, retainedFrom, spillPath
}

func (o *managedProcessOutput) remove() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.removed {
		return
	}
	o.removed = true
	_ = o.file.Close()
	o.notifyLocked()
}

func (o *managedProcessOutput) notifyLocked() {
	close(o.updated)
	o.updated = make(chan struct{})
}

func startManagedProcess(request ManagedProcessRequest) (*ManagedProcess, error) {
	env, err := managedProcessEnvironment(request.Env)
	if err != nil {
		return nil, invalidManagedProcessRequest(err)
	}
	cwd, err := managedProcessWorkingDir(request.Cwd, true)
	if err != nil {
		return nil, invalidManagedProcessRequest(err)
	}
	executable, err := resolveManagedExecutable(request.Argv[0], cwd, env)
	if err != nil {
		return nil, invalidManagedProcessRequest(err)
	}

	id := uuid.NewString()
	dir := filepath.Join(os.TempDir(), "opensandbox-execd", "processes", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create managed process directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	stdout, err := newManagedProcessOutput(filepath.Join(dir, "stdout"), request.StdoutRetentionBytes)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create managed stdout retention: %w", err)
	}
	stderr, err := newManagedProcessOutput(filepath.Join(dir, "stderr"), request.StderrRetentionBytes)
	if err != nil {
		stdout.remove()
		cleanup()
		return nil, fmt.Errorf("create managed stderr retention: %w", err)
	}

	stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW, err := managedProcessPipes()
	if err != nil {
		stdout.remove()
		stderr.remove()
		cleanup()
		return nil, err
	}
	closePipes := func() {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
	}

	cmd := exec.Command(executable)
	cmd.Path = executable
	cmd.Args = append([]string(nil), request.Argv...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	if err := configureManagedProcess(cmd); err != nil {
		closePipes()
		stdout.remove()
		stderr.remove()
		cleanup()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		closePipes()
		stdout.remove()
		stderr.remove()
		cleanup()
		return nil, fmt.Errorf("start managed process: %w", err)
	}
	_ = stdinR.Close()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	groupStart, err := managedProcessStartIdentity(cmd.Process.Pid)
	if err != nil {
		_ = forceManagedProcessGroup(cmd.Process.Pid, 0)
		_ = cmd.Wait()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stderrR.Close()
		stdout.remove()
		stderr.remove()
		cleanup()
		return nil, fmt.Errorf("record managed process identity: %w", err)
	}

	process := &ManagedProcess{
		id:            id,
		operationID:   request.OperationID,
		pid:           cmd.Process.Pid,
		groupStart:    groupStart,
		grace:         request.Grace,
		dir:           dir,
		done:          make(chan struct{}),
		treeDone:      make(chan struct{}),
		stdin:         stdinW,
		stdout:        stdout,
		stderr:        stderr,
		stdoutReader:  stdoutR,
		stderrReader:  stderrR,
		terminateDone: make(chan struct{}),
	}
	go process.pumpOutput(stdoutR, stdout)
	go process.pumpOutput(stderrR, stderr)
	go process.wait(cmd)
	go process.observeTree()
	return process, nil
}

func managedProcessPipes() (stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW *os.File, err error) {
	stdinR, stdinW, err = os.Pipe()
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("create managed stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err = os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("create managed stdout pipe: %w", err)
	}
	stderrR, stderrW, err = os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("create managed stderr pipe: %w", err)
	}
	return stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW, nil
}

func (p *ManagedProcess) pumpOutput(reader *os.File, output *managedProcessOutput) {
	_, err := io.Copy(output, reader)
	_ = reader.Close()
	output.finish(err == nil)
}

func (p *ManagedProcess) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	exitCode, signalName := managedProcessExitOutcome(cmd.ProcessState, err)
	p.mu.Lock()
	p.exitCode = exitCode
	p.signal = signalName
	p.topLevelExited = true
	p.mu.Unlock()
	p.closeInputAfterExit()
	close(p.done)
}

func (p *ManagedProcess) closeInputAfterExit() {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if !p.stdinClosed {
		p.stdinClosed = true
		_ = p.stdin.Close()
	}
}

func (p *ManagedProcess) observeTree() {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		live, err := managedProcessGroupLive(p.pid, p.groupStart)
		if err == nil && !live {
			p.markTreeEmpty()
			return
		}
		<-ticker.C
	}
}

func (p *ManagedProcess) markTreeEmpty() {
	p.mu.Lock()
	if p.treeEmpty {
		p.mu.Unlock()
		return
	}
	p.treeEmpty = true
	close(p.treeDone)
	p.mu.Unlock()
}

func (p *ManagedProcess) startTermination(graceOverride *time.Duration) {
	p.terminateOnce.Do(func() {
		grace := p.grace
		if graceOverride != nil {
			grace = *graceOverride
		}
		go func() {
			defer close(p.terminateDone)
			if grace > 0 {
				if err := p.signalGroup(managedProcessSignalTerm); err == nil {
					timer := time.NewTimer(grace)
					select {
					case <-p.treeDone:
						timer.Stop()
						<-p.done
						return
					case <-timer.C:
					}
				}
			}
			if err := p.signalGroup(managedProcessSignalKill); err != nil {
				p.setTerminationError(err)
			}
			<-p.treeDone
			<-p.done
		}()
	})
}

func (p *ManagedProcess) signalGroup(signal managedProcessSignal) error {
	live, err := managedProcessGroupLive(p.pid, p.groupStart)
	if err != nil {
		return err
	}
	if !live {
		p.markTreeEmpty()
		return nil
	}
	if err := signalManagedProcessGroup(p.pid, p.groupStart, signal); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			p.markTreeEmpty()
			return nil
		}
		return err
	}
	return nil
}

func (p *ManagedProcess) forceKill() {
	if err := p.signalGroup(managedProcessSignalKill); err != nil {
		p.setTerminationError(err)
	}
}

func (p *ManagedProcess) setTerminationError(err error) {
	p.terminateMu.Lock()
	defer p.terminateMu.Unlock()
	if p.terminateErr == nil {
		p.terminateErr = err
	}
}

func (p *ManagedProcess) terminationError() error {
	p.terminateMu.Lock()
	defer p.terminateMu.Unlock()
	return p.terminateErr
}

func (p *ManagedProcess) remove() {
	p.stdinMu.Lock()
	if p.stdinAttachment != nil {
		p.stdinAttachment.releaseLocked()
		p.stdinAttachment = nil
	}
	p.stdinMu.Unlock()
	_ = p.stdoutReader.Close()
	_ = p.stderrReader.Close()
	p.stdout.remove()
	p.stderr.remove()
	_ = os.RemoveAll(p.dir)
}

func validateManagedProcessRequest(request ManagedProcessRequest) error {
	if request.OperationID == "" {
		return errors.New("managed process operation id is required")
	}
	if len(request.Argv) == 0 || request.Argv[0] == "" {
		return errors.New("managed process argv must not be empty")
	}
	if request.StdoutRetentionBytes < 0 || request.StderrRetentionBytes < 0 {
		return errors.New("managed process retention bytes must not be negative")
	}
	if request.Grace < 0 {
		return errors.New("managed process grace must not be negative")
	}
	for _, arg := range request.Argv {
		if strings.ContainsRune(arg, 0) {
			return errors.New("managed process argv must not contain NUL")
		}
	}
	if !filepath.IsAbs(request.Cwd) {
		return errors.New("managed process cwd must be absolute")
	}
	return nil
}

func invalidManagedProcessRequest(err error) error {
	return fmt.Errorf("%w: %v", ErrManagedProcessInvalidRequest, err)
}

func cloneManagedProcessRequest(request ManagedProcessRequest) ManagedProcessRequest {
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

func managedProcessWorkingDir(cwd string, required bool) (string, error) {
	if cwd == "" {
		if required {
			return "", errors.New("managed process cwd must be absolute")
		}
		current, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get execd working directory: %w", err)
		}
		return current, nil
	}
	if !filepath.IsAbs(cwd) {
		return "", errors.New("managed process cwd must be absolute")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("stat managed process cwd: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("managed process cwd is not a directory")
	}
	return filepath.Clean(cwd), nil
}

func managedProcessEnvironment(patch map[string]*string) ([]string, error) {
	base := mergeEnvs(os.Environ(), loadExtraEnvFromFile())
	env := make(map[string]string, len(base)+len(patch))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok && !scrubManagedProcessEnv(key) {
			env[key] = value
		}
	}
	for key, value := range patch {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return nil, fmt.Errorf("invalid managed process environment name %q", key)
		}
		if value == nil {
			delete(env, key)
			continue
		}
		if strings.ContainsRune(*value, 0) {
			return nil, fmt.Errorf("managed process environment %q contains NUL", key)
		}
		env[key] = *value
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result, nil
}

func scrubManagedProcessEnv(key string) bool {
	upper := strings.ToUpper(key)
	if strings.HasPrefix(upper, "EXECD_") || strings.HasPrefix(upper, "_EXECD_") ||
		strings.HasPrefix(upper, "OPENSANDBOX_EXECD_") || strings.HasPrefix(upper, "JUPYTER_") ||
		strings.HasPrefix(upper, "DSH_") {
		return true
	}
	for _, suffix := range []string{
		"API_KEY", "APIKEY", "TOKEN", "AUTH_TOKEN", "ACCESS_TOKEN", "REFRESH_TOKEN",
		"SECRET", "API_SECRET", "PASSWORD", "PASSWD", "PRIVATE_KEY", "CLIENT_SECRET",
		"AUTH_KEY", "SECRET_ID", "CREDENTIAL", "CREDENTIALS", "ACCESS_KEY",
		"ACCESS_KEY_ID", "ACCESS_KEY_SECRET", "SECRET_ACCESS_KEY", "SECRET_KEY",
		"SESSION_TOKEN", "ACCOUNT_KEY", "SHARED_ACCESS_KEY", "STORAGE_KEY",
	} {
		if upper == suffix || strings.HasSuffix(upper, "_"+suffix) {
			return true
		}
	}
	return false
}

func resolveManagedExecutable(executable, cwd string, env []string) (string, error) {
	if executable == "" || strings.ContainsRune(executable, 0) {
		return "", errors.New("managed process executable is required")
	}
	if filepath.IsAbs(executable) {
		return validateManagedExecutable(filepath.Clean(executable))
	}
	if strings.ContainsRune(executable, filepath.Separator) {
		return "", errors.New("managed process executable with a path separator must be absolute")
	}
	pathValue := ""
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			pathValue = value
			break
		}
	}
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			directory = cwd
		} else if !filepath.IsAbs(directory) {
			directory = filepath.Join(cwd, directory)
		}
		candidate, err := validateManagedExecutable(filepath.Join(directory, executable))
		if err == nil {
			absolute, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", absErr
			}
			return absolute, nil
		}
	}
	return "", fmt.Errorf("managed process executable %q not found in PATH", executable)
}

func validateManagedExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat managed process executable %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("managed process executable %q is not an executable regular file", path)
	}
	return path, nil
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
