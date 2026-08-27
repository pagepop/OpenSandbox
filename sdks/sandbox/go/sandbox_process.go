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
	"fmt"
)

// ManagedProcessHandle defers process identity until execd publishes a successful create.
type ManagedProcessHandle struct {
	client *ExecdClient
	ready  chan struct{}
	status *ManagedProcessStatus
	err    error
}

// WaitReady waits for remote publication and returns the numeric diagnostic PID.
func (h *ManagedProcessHandle) WaitReady(ctx context.Context) (*ManagedProcessReady, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.ready:
		if h.err != nil {
			return nil, h.err
		}
		return &ManagedProcessReady{PID: *h.status.PID}, nil
	}
}

// ProcessID returns the opaque identity after publication.
func (h *ManagedProcessHandle) ProcessID() (string, bool) {
	select {
	case <-h.ready:
		if h.err != nil {
			return "", false
		}
		return h.status.ProcessID, true
	default:
		return "", false
	}
}

// PID returns the numeric diagnostic PID after publication.
func (h *ManagedProcessHandle) PID() (int, bool) {
	select {
	case <-h.ready:
		if h.err != nil {
			return 0, false
		}
		return *h.status.PID, true
	default:
		return 0, false
	}
}

// Get waits for publication and returns the current process status.
func (h *ManagedProcessHandle) Get(ctx context.Context) (*ManagedProcessStatus, error) {
	processID, err := h.waitProcessID(ctx)
	if err != nil {
		return nil, err
	}
	return h.client.GetManagedProcess(ctx, processID)
}

// Terminate waits for publication, then starts or joins group termination.
func (h *ManagedProcessHandle) Terminate(ctx context.Context, options *TerminateManagedProcessOptions) (*ManagedProcessStatus, error) {
	processID, err := h.waitProcessID(ctx)
	if err != nil {
		return nil, err
	}
	return h.client.TerminateManagedProcess(ctx, processID, options)
}

// Delete waits for publication and removes a quiescent process record.
func (h *ManagedProcessHandle) Delete(ctx context.Context) error {
	processID, err := h.waitProcessID(ctx)
	if err != nil {
		return err
	}
	return h.client.DeleteManagedProcess(ctx, processID)
}

// Attach waits for publication and opens raw stdin, stdout, and stderr transport.
func (h *ManagedProcessHandle) Attach(ctx context.Context, options ManagedProcessAttachOptions) (*ManagedProcessAttachment, error) {
	processID, err := h.waitProcessID(ctx)
	if err != nil {
		return nil, err
	}
	return h.client.AttachManagedProcess(ctx, processID, options)
}

func (h *ManagedProcessHandle) waitProcessID(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-h.ready:
		if h.err != nil {
			return "", h.err
		}
		return h.status.ProcessID, nil
	}
}

// ResolveExecutable resolves and validates an executable inside this sandbox.
func (s *Sandbox) ResolveExecutable(ctx context.Context, request ResolveExecutableRequest) (*ResolveExecutableResponse, error) {
	if s.execd == nil {
		return nil, fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.ResolveExecutable(ctx, request)
}

// StartManagedProcess starts a create request immediately and returns before publication.
func (s *Sandbox) StartManagedProcess(ctx context.Context, request CreateManagedProcessRequest) *ManagedProcessHandle {
	handle := &ManagedProcessHandle{client: s.execd, ready: make(chan struct{})}
	if s.execd == nil {
		handle.err = fmt.Errorf("opensandbox: execd client not initialized")
		close(handle.ready)
		return handle
	}

	go func() {
		status, err := s.execd.CreateManagedProcess(ctx, request)
		if err == nil && (status == nil || status.ProcessID == "" || status.PID == nil) {
			err = fmt.Errorf("opensandbox: create managed process response omitted processId or pid")
		}
		handle.status = status
		handle.err = err
		close(handle.ready)
	}()
	return handle
}

// GetManagedProcess returns a managed process status by opaque ID.
func (s *Sandbox) GetManagedProcess(ctx context.Context, processID string) (*ManagedProcessStatus, error) {
	if s.execd == nil {
		return nil, fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.GetManagedProcess(ctx, processID)
}

// TerminateManagedProcess starts or joins group termination by opaque ID.
func (s *Sandbox) TerminateManagedProcess(ctx context.Context, processID string, options *TerminateManagedProcessOptions) (*ManagedProcessStatus, error) {
	if s.execd == nil {
		return nil, fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.TerminateManagedProcess(ctx, processID, options)
}

// DeleteManagedProcess removes a quiescent process record by opaque ID.
func (s *Sandbox) DeleteManagedProcess(ctx context.Context, processID string) error {
	if s.execd == nil {
		return fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.DeleteManagedProcess(ctx, processID)
}

// AttachManagedProcess opens raw stdin, stdout, and stderr transport by opaque ID.
func (s *Sandbox) AttachManagedProcess(ctx context.Context, processID string, options ManagedProcessAttachOptions) (*ManagedProcessAttachment, error) {
	if s.execd == nil {
		return nil, fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.AttachManagedProcess(ctx, processID, options)
}
