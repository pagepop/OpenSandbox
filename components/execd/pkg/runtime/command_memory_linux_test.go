//go:build linux

// Copyright 2025 Alibaba Group Holding Ltd.
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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alibaba/opensandbox/execd/pkg/jupyter/execute"
	"github.com/stretchr/testify/require"
)

func TestCommandMemoryRejectsInvalidPoolBeforeLaunch(t *testing.T) {
	t.Setenv(commandMemoryPoolEnv, t.TempDir())
	t.Setenv(commandMemoryLimitEnv, "536870912")
	output := filepath.Join(t.TempDir(), "ran")
	cmd := exec.Command("sh", "-c", "echo started > \"$1\"", "test", output)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	group, err := startCommandWithMemoryBudget(cmd)
	require.ErrorContains(t, err, "delegated cgroup")
	require.Nil(t, group)
	require.Nil(t, cmd.Process)
	require.NoFileExists(t, output)
}

// The operator must supply an isolated v2 subtree in a 2 GiB sandbox before
// enabling this smoke. Never run this against the host's cgroup hierarchy.
func TestCommandMemoryIsolationIn2GiBSandbox(t *testing.T) {
	if os.Getenv("EXECD_TEST_COMMAND_MEMORY_ISOLATION") != "1" {
		t.Skip("requires a dedicated delegated 2 GiB sandbox")
	}
	pool := os.Getenv(commandMemoryPoolEnv)
	limit, err := parseMemoryLimit(os.Getenv(commandMemoryLimitEnv))
	require.NoError(t, err)
	require.NoError(t, validateCommandMemoryPool(pool, limit))
	outer, err := readMemoryLimit(filepath.Dir(pool))
	require.NoError(t, err)
	require.EqualValues(t, 2<<30, outer)
	_, err = exec.LookPath("python3")
	require.NoError(t, err)

	c := NewController("", "")
	executeCommand := func(program string) (*CommandStatus, *execute.ErrorOutput, string) {
		var id string
		var failure *execute.ErrorOutput
		var output strings.Builder
		request := &ExecuteCodeRequest{
			Language: Command, Code: program, Cwd: t.TempDir(), Timeout: 30 * time.Second,
			Hooks: ExecuteResultHook{
				OnExecuteInit:     func(value string) { id = value },
				OnExecuteStdout:   func(value string) { output.WriteString(value) },
				OnExecuteStderr:   func(string) {},
				OnExecuteError:    func(value *execute.ErrorOutput) { failure = value },
				OnExecuteComplete: func(time.Duration) {},
			},
		}
		require.NoError(t, c.Execute(request))
		status, err := c.GetCommandStatus(id)
		require.NoError(t, err)
		require.False(t, status.Running)
		require.NotNil(t, status.ExitCode)
		return status, failure, output.String()
	}

	status, failure, _ := executeCommand(`python3 -u - <<'PYTHON'
blocks = []
while True:
    data = bytearray(16 * 1024 * 1024)
    for i in range(0, len(data), 4096):
        data[i] = 1
    blocks.append(data)
PYTHON
`)
	require.Equal(t, commandMemoryExitCode, *status.ExitCode)
	require.NotNil(t, failure)
	require.NotNil(t, failure.ExitCode)
	require.Equal(t, commandMemoryExitCode, *failure.ExitCode)
	require.Equal(t, []string{errCommandMemoryLimit.Error()}, failure.Traceback)

	status, failure, output := executeCommand("printf 'same-execd\n'")
	require.Equal(t, 0, *status.ExitCode)
	require.Nil(t, failure)
	require.Equal(t, "same-execd\n", output)
}
