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

//go:build windows
// +build windows

package runtime

import (
	"os"
	"os/exec"
)

func startManagedTerminalPTY(_ *exec.Cmd, _, _ uint16) (*os.File, error) {
	return nil, ErrManagedTerminalUnsupported
}

func resizeManagedTerminal(_ *os.File, _, _ uint16) error {
	return ErrManagedTerminalUnsupported
}

func foregroundManagedTerminal(_ *os.File, _ int, _ uint64) (int, error) {
	return 0, ErrManagedTerminalUnsupported
}

func signalManagedTerminalForeground(_ int, _ uint64, _ int, _ ManagedTerminalSignal) error {
	return ErrManagedTerminalUnsupported
}

func managedTerminalSessionLive(_ int, _ uint64) (bool, error) {
	return false, ErrManagedTerminalUnsupported
}

func signalManagedTerminalSession(_ int, _ uint64, _ ManagedTerminalSignal) error {
	return ErrManagedTerminalUnsupported
}
