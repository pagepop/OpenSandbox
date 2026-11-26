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
	"github.com/stretchr/testify/assert"
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
// enabling these tests. Never run them against the host's cgroup hierarchy.
func requireCommandMemoryFixture(t *testing.T) {
	t.Helper()
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
}

func TestCommandMemoryCleansUpDetachedChild(t *testing.T) {
	requireCommandMemoryFixture(t)
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	completed := filepath.Join(dir, "completed")
	cmd := exec.Command("sh", "-c", `
		(
			attempt=0
			while [ "$attempt" -lt 200 ]; do
				if [ -f "$1" ]; then
					touch "$2"
					exit
				fi
				attempt=$((attempt + 1))
				sleep 0.05
			done
		) </dev/null >/dev/null 2>&1 &
	`, "sh", release, completed)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	process, group, err := launchCommandWithMemoryBudget(cmd)
	require.NoError(t, err)
	require.NotNil(t, group)
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	require.NoError(t, process.Wait())

	events, err := os.ReadFile(filepath.Join(group.dir, "cgroup.events"))
	require.NoError(t, err)
	require.Contains(t, string(events), "populated 1\n")
	require.False(t, group.finish())
	require.DirExists(t, group.dir)
	require.NoFileExists(t, completed)

	require.NoError(t, os.WriteFile(release, nil, 0o600))
	require.Eventually(t, func() bool {
		_, err := os.Stat(group.dir)
		return os.IsNotExist(err)
	}, 4*time.Second, 25*time.Millisecond, "the empty cgroup should be reclaimed")
	require.FileExists(t, completed)
}

const exhaustCommandMemory = `python3 -u - <<'PYTHON'
blocks = []
while True:
    data = bytearray(16 * 1024 * 1024)
    for i in range(0, len(data), 4096):
        data[i] = 1
    blocks.append(data)
PYTHON
`

func TestCommandMemoryIsolationIn2GiBSandbox(t *testing.T) {
	requireCommandMemoryFixture(t)
	_, err := exec.LookPath("python3")
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

	status, failure, _ := executeCommand(exhaustCommandMemory)
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

func TestCommandMemoryBackgroundOOM(t *testing.T) {
	requireCommandMemoryFixture(t)
	_, err := exec.LookPath("python3")
	require.NoError(t, err)
	c := NewController("", "")
	cwd := t.TempDir()
	runBackground := func(program string) *CommandStatus {
		var id string
		require.NoError(t, c.Execute(&ExecuteCodeRequest{
			Language: BackgroundCommand, Code: program, Cwd: cwd, Timeout: 20 * time.Second,
			Hooks: ExecuteResultHook{
				OnExecuteInit:     func(value string) { id = value },
				OnExecuteComplete: func(time.Duration) {},
			},
		}))
		require.EventuallyWithT(t, func(collect *assert.CollectT) {
			status, err := c.GetCommandStatus(id)
			require.NoError(collect, err)
			require.False(collect, status.Running)
		}, 25*time.Second, 25*time.Millisecond)
		status, err := c.GetCommandStatus(id)
		require.NoError(t, err)
		require.NotNil(t, status.ExitCode)
		return status
	}

	status := runBackground(exhaustCommandMemory)
	require.Equal(t, commandMemoryExitCode, *status.ExitCode)
	require.Equal(t, errCommandMemoryLimit.Error(), status.Error)

	status = runBackground("printf 'same-execd\\n'")
	require.Equal(t, 0, *status.ExitCode)
	require.Empty(t, status.Error)
	output, _, err := c.SeekBackgroundCommandOutput(status.Session, 0)
	require.NoError(t, err)
	require.Equal(t, "same-execd\n", string(output))
}
