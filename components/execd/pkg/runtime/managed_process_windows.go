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

func configureManagedProcess(_ *exec.Cmd) error { return ErrManagedProcessUnsupported }

func managedProcessStartIdentity(_ int) (uint64, error) { return 0, ErrManagedProcessUnsupported }

func managedProcessGroupLive(_ int, _ uint64) (bool, error) {
	return false, ErrManagedProcessUnsupported
}

func signalManagedProcessGroup(_ int, _ uint64, _ managedProcessSignal) error {
	return ErrManagedProcessUnsupported
}

func forceManagedProcessGroup(_ int, _ uint64) error { return ErrManagedProcessUnsupported }

func managedProcessExitOutcome(_ *os.ProcessState, _ error) (*int, *string) {
	return nil, nil
}
