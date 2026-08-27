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

// ManagedTerminalHandle defers terminal identity until execd publishes a successful create.
type ManagedTerminalHandle struct {
	client *ExecdClient
	ready  chan struct{}
	status *ManagedTerminalStatus
	err    error
}

// WaitReady waits for remote publication and returns the numeric diagnostic PID.
func (h *ManagedTerminalHandle) WaitReady(ctx context.Context) (*ManagedTerminalReady, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.ready:
		if h.err != nil {
			return nil, h.err
		}
		return &ManagedTerminalReady{PID: *h.status.PID}, nil
	}
}

// TerminalID returns the opaque identity after publication.
func (h *ManagedTerminalHandle) TerminalID() (string, bool) {
	select {
	case <-h.ready:
		if h.err != nil {
			return "", false
		}
		return h.status.TerminalID, true
	default:
		return "", false
	}
}

// PID returns the numeric diagnostic PID after publication.
func (h *ManagedTerminalHandle) PID() (int, bool) {
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

// Get waits for publication and returns the current terminal status.
func (h *ManagedTerminalHandle) Get(ctx context.Context) (*ManagedTerminalStatus, error) {
	terminalID, err := h.waitTerminalID(ctx)
	if err != nil {
		return nil, err
	}
	return h.client.GetManagedTerminal(ctx, terminalID)
}

// Foreground waits for publication and returns the current foreground process group.
func (h *ManagedTerminalHandle) Foreground(ctx context.Context) (*ManagedTerminalForeground, error) {
	terminalID, err := h.waitTerminalID(ctx)
	if err != nil {
		return nil, err
	}
	return h.client.GetManagedTerminalForeground(ctx, terminalID)
}

// SignalForeground waits for publication and signals the current foreground process group.
func (h *ManagedTerminalHandle) SignalForeground(ctx context.Context, signal ManagedTerminalSignal) error {
	terminalID, err := h.waitTerminalID(ctx)
	if err != nil {
		return err
	}
	return h.client.SignalManagedTerminalForeground(ctx, terminalID, signal)
}

// Terminate waits for publication, then starts or joins group termination.
func (h *ManagedTerminalHandle) Terminate(ctx context.Context, options *TerminateManagedTerminalOptions) (*ManagedTerminalStatus, error) {
	terminalID, err := h.waitTerminalID(ctx)
	if err != nil {
		return nil, err
	}
	return h.client.TerminateManagedTerminal(ctx, terminalID, options)
}

// Delete waits for publication and removes a quiescent terminal record.
func (h *ManagedTerminalHandle) Delete(ctx context.Context) error {
	terminalID, err := h.waitTerminalID(ctx)
	if err != nil {
		return err
	}
	return h.client.DeleteManagedTerminal(ctx, terminalID)
}

// Attach waits for publication and opens raw terminal input and merged output.
func (h *ManagedTerminalHandle) Attach(ctx context.Context, options ManagedTerminalAttachOptions) (*ManagedTerminalAttachment, error) {
	terminalID, err := h.waitTerminalID(ctx)
	if err != nil {
		return nil, err
	}
	return h.client.AttachManagedTerminal(ctx, terminalID, options)
}

func (h *ManagedTerminalHandle) waitTerminalID(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-h.ready:
		if h.err != nil {
			return "", h.err
		}
		return h.status.TerminalID, nil
	}
}

// StartManagedTerminal starts a create request immediately and returns before publication.
func (s *Sandbox) StartManagedTerminal(ctx context.Context, request CreateManagedTerminalRequest) *ManagedTerminalHandle {
	handle := &ManagedTerminalHandle{client: s.execd, ready: make(chan struct{})}
	if s.execd == nil {
		handle.err = fmt.Errorf("opensandbox: execd client not initialized")
		close(handle.ready)
		return handle
	}

	go func() {
		status, err := s.execd.CreateManagedTerminal(ctx, request)
		if err == nil && (status == nil || status.TerminalID == "" || status.PID == nil) {
			err = fmt.Errorf("opensandbox: create managed terminal response omitted terminalId or pid")
		}
		handle.status = status
		handle.err = err
		close(handle.ready)
	}()
	return handle
}

// GetManagedTerminal returns a managed terminal status by opaque ID.
func (s *Sandbox) GetManagedTerminal(ctx context.Context, terminalID string) (*ManagedTerminalStatus, error) {
	if s.execd == nil {
		return nil, fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.GetManagedTerminal(ctx, terminalID)
}

// GetManagedTerminalForeground returns the current foreground group by opaque ID.
func (s *Sandbox) GetManagedTerminalForeground(ctx context.Context, terminalID string) (*ManagedTerminalForeground, error) {
	if s.execd == nil {
		return nil, fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.GetManagedTerminalForeground(ctx, terminalID)
}

// SignalManagedTerminalForeground signals the current foreground group by opaque ID.
func (s *Sandbox) SignalManagedTerminalForeground(ctx context.Context, terminalID string, signal ManagedTerminalSignal) error {
	if s.execd == nil {
		return fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.SignalManagedTerminalForeground(ctx, terminalID, signal)
}

// TerminateManagedTerminal starts or joins group termination by opaque ID.
func (s *Sandbox) TerminateManagedTerminal(ctx context.Context, terminalID string, options *TerminateManagedTerminalOptions) (*ManagedTerminalStatus, error) {
	if s.execd == nil {
		return nil, fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.TerminateManagedTerminal(ctx, terminalID, options)
}

// DeleteManagedTerminal removes a quiescent terminal record by opaque ID.
func (s *Sandbox) DeleteManagedTerminal(ctx context.Context, terminalID string) error {
	if s.execd == nil {
		return fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.DeleteManagedTerminal(ctx, terminalID)
}

// AttachManagedTerminal opens raw terminal input and merged output by opaque ID.
func (s *Sandbox) AttachManagedTerminal(ctx context.Context, terminalID string, options ManagedTerminalAttachOptions) (*ManagedTerminalAttachment, error) {
	if s.execd == nil {
		return nil, fmt.Errorf("opensandbox: execd client not initialized")
	}
	return s.execd.AttachManagedTerminal(ctx, terminalID, options)
}
