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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alibaba/opensandbox/execd/pkg/log"
	"golang.org/x/sys/unix"
)

const cgroupMount = "/sys/fs/cgroup"

type commandMemoryGroup struct {
	dir string
}

func startCommandWithMemoryBudget(cmd *exec.Cmd) (*commandMemoryGroup, error) {
	if !commandMemoryEnabled() {
		return nil, cmd.Start()
	}
	pool := os.Getenv(commandMemoryPoolEnv)
	limit, err := parseMemoryLimit(os.Getenv(commandMemoryLimitEnv))
	if err != nil {
		return nil, fmt.Errorf("invalid command memory budget: %w", err)
	}
	if err := validateCommandMemoryPool(pool, limit); err != nil {
		return nil, fmt.Errorf("command memory pool unavailable: %w", err)
	}

	dir, err := os.MkdirTemp(pool, "command-")
	if err != nil {
		return nil, fmt.Errorf("create command memory group: %w", err)
	}
	started := false
	defer func() {
		if !started {
			removeCommandMemoryGroup(dir, false)
		}
	}()

	settings := [][2]string{
		{"memory.max", strconv.FormatUint(limit, 10)},
		{"memory.oom.group", "1"},
	}
	for _, setting := range settings {
		if err := setCommandMemoryControl(filepath.Join(dir, setting[0]), setting[1]); err != nil {
			return nil, fmt.Errorf("set command memory group %s: %w", setting[0], err)
		}
	}
	group, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open command memory group: %w", err)
	}
	defer group.Close()
	// Joining at clone time prevents any shell instructions or descendants from
	// running in execd's own group before the memory limit takes effect.
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(group.Fd())
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command in its memory group: %w", err)
	}
	started = true
	return &commandMemoryGroup{dir: dir}, nil
}

func setCommandMemoryControl(file, value string) error {
	fd, err := os.OpenFile(file, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer fd.Close()
	_, err = fd.WriteString(value)
	return err
}

func validateCommandMemoryPool(pool string, limit uint64) error {
	if filepath.Clean(pool) != pool || !strings.HasPrefix(pool, cgroupMount+"/") {
		return fmt.Errorf("%s must name a delegated cgroup below %s", commandMemoryPoolEnv, cgroupMount)
	}
	canonical, err := filepath.EvalSymlinks(pool)
	if err != nil {
		return err
	}
	if canonical != pool {
		return fmt.Errorf("delegated cgroup path must not use symlinks")
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(pool, &stat); err != nil {
		return err
	}
	if stat.Type != unix.CGROUP2_SUPER_MAGIC {
		return fmt.Errorf("delegated directory is not a cgroup v2 filesystem")
	}

	aggregate, err := readMemoryLimit(pool)
	if err != nil {
		return fmt.Errorf("read aggregate memory budget: %w", err)
	}
	outer, err := readMemoryLimit(filepath.Dir(pool))
	if err != nil {
		return fmt.Errorf("read sandbox memory budget: %w", err)
	}
	if limit >= aggregate || aggregate >= outer {
		return fmt.Errorf("command budget must be below aggregate budget, which must be below sandbox budget")
	}
	group, err := os.ReadFile(filepath.Join(pool, "memory.oom.group"))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(group)) != "0" {
		return fmt.Errorf("aggregate group must not kill all user commands together")
	}
	controllers, err := os.ReadFile(filepath.Join(pool, "cgroup.subtree_control"))
	if err != nil {
		return err
	}
	if !strings.Contains(" "+strings.Join(strings.Fields(string(controllers)), " ")+" ", " memory ") {
		return fmt.Errorf("memory controller is not delegated to command groups")
	}

	membership, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return err
	}
	return validateCommandMemoryMembership(strings.TrimPrefix(pool, cgroupMount), string(membership))
}

func readMemoryLimit(dir string) (uint64, error) {
	data, err := os.ReadFile(filepath.Join(dir, "memory.max"))
	if err != nil {
		return 0, err
	}
	return parseMemoryLimit(string(data))
}

func (c *commandMemoryGroup) finish() bool {
	if c == nil {
		return false
	}
	events, err := os.ReadFile(filepath.Join(c.dir, "memory.events.local"))
	if err != nil {
		log.Warn("read command memory events: %v", err)
		removeCommandMemoryGroup(c.dir, false)
		return false
	}
	oomKilled := commandMemoryOOM(string(events))
	removeCommandMemoryGroup(c.dir, oomKilled)
	return oomKilled
}

func removeCommandMemoryGroup(dir string, oomKilled bool) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		err := os.Remove(dir)
		if err == nil || os.IsNotExist(err) {
			return
		}
		// Following a group OOM, the reaped command may precede its other
		// killed descendants. Wait briefly for those tasks to leave the group.
		// Do not wait or kill deliberately detached children on normal exit.
		if !oomKilled || !errors.Is(err, unix.EBUSY) || !time.Now().Before(deadline) {
			log.Warn("remove command memory group: %v", err)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
