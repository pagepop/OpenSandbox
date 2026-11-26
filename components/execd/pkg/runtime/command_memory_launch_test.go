//go:build !windows

// Copyright 2026 The OpenSandbox Authors
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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandMemoryManagedLaunchWithoutBudget(t *testing.T) {
	for _, key := range []string{commandMemoryPoolEnv, commandMemoryLimitEnv} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	for _, code := range []int{0, 42} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
			process, group, err := launchCommandWithMemoryBudget(cmd)
			require.NoError(t, err)
			require.NotNil(t, process)
			require.Nil(t, group)
			err = process.Wait()
			if code == 0 {
				require.NoError(t, err)
			} else {
				var exitError exitCoder
				require.ErrorAs(t, err, &exitError)
				require.Equal(t, code, exitError.ExitCode())
			}
		})
	}
}

func TestCommandMemoryManagedLaunchRejectsInvalidBudget(t *testing.T) {
	t.Setenv(commandMemoryPoolEnv, t.TempDir())
	t.Setenv(commandMemoryLimitEnv, "536870912")
	cwd := t.TempDir()
	cmd := exec.Command("sh", "-c", "printf started > ran")
	cmd.Dir = cwd
	process, group, err := launchCommandWithMemoryBudget(cmd)
	require.Error(t, err)
	require.Nil(t, process)
	require.Nil(t, group)
	require.Nil(t, cmd.Process)
	require.NoFileExists(t, filepath.Join(cwd, "ran"))
}
