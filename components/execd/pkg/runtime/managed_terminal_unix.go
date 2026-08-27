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

//go:build !windows
// +build !windows

package runtime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"sort"
	"strconv"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func startManagedTerminalPTY(cmd *exec.Cmd, rows, cols uint16) (*os.File, error) {
	return pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
}

func resizeManagedTerminal(ptmx *os.File, rows, cols uint16) error {
	return pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

func foregroundManagedTerminal(ptmx *os.File, sessionID int, startTime uint64) (int, error) {
	group, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return 0, fmt.Errorf("inspect managed terminal foreground group: %w", err)
	}
	if group <= 0 {
		return 0, errors.New("managed terminal has no foreground process group")
	}
	if goruntime.GOOS == "linux" {
		groups, err := managedTerminalSessionGroups(sessionID, startTime)
		if err != nil {
			return 0, err
		}
		for _, candidate := range groups {
			if candidate == group {
				return group, nil
			}
		}
		return 0, os.ErrProcessDone
	}
	return group, nil
}

func signalManagedTerminalForeground(sessionID int, startTime uint64, group int, signal ManagedTerminalSignal) error {
	value, err := managedTerminalSignalValue(signal)
	if err != nil {
		return err
	}
	if goruntime.GOOS == "linux" {
		groups, err := managedTerminalSessionGroups(sessionID, startTime)
		if err != nil {
			return err
		}
		found := false
		for _, candidate := range groups {
			if candidate == group {
				found = true
				break
			}
		}
		if !found {
			return os.ErrProcessDone
		}
	}
	if err := syscall.Kill(-group, value); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

func managedTerminalSessionLive(sessionID int, startTime uint64) (bool, error) {
	groups, err := managedTerminalSessionGroups(sessionID, startTime)
	return len(groups) > 0, err
}

func signalManagedTerminalSession(sessionID int, startTime uint64, signal ManagedTerminalSignal) error {
	value, err := managedTerminalSignalValue(signal)
	if err != nil {
		return err
	}
	groups, err := managedTerminalSessionGroups(sessionID, startTime)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return os.ErrProcessDone
	}
	for _, group := range groups {
		if err := syscall.Kill(-group, value); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}
	return nil
}

func managedTerminalSignalValue(signal ManagedTerminalSignal) (syscall.Signal, error) {
	switch signal {
	case ManagedTerminalSignalInterrupt:
		return syscall.SIGINT, nil
	case ManagedTerminalSignalTerminate:
		return syscall.SIGTERM, nil
	case ManagedTerminalSignalKill:
		return syscall.SIGKILL, nil
	case ManagedTerminalSignalStop:
		return syscall.SIGTSTP, nil
	case ManagedTerminalSignalHangup:
		return syscall.SIGHUP, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrManagedTerminalSignal, signal)
	}
}

func managedTerminalSessionGroups(sessionID int, startTime uint64) ([]int, error) {
	if goruntime.GOOS != "linux" {
		live, err := managedProcessGroupLive(sessionID, startTime)
		if err != nil || !live {
			return nil, err
		}
		return []int{sessionID}, nil
	}

	leader, err := readManagedProcStat(sessionID)
	if err == nil && leader.startTime != startTime {
		return nil, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	groups := make(map[int]struct{})
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := readManagedProcStat(pid)
		if err != nil || stat.session != sessionID || terminalProcessGone(stat.state) {
			continue
		}
		groups[stat.processGroup] = struct{}{}
	}
	result := make([]int, 0, len(groups))
	for group := range groups {
		result = append(result, group)
	}
	sort.Ints(result)
	return result, nil
}

func terminalProcessGone(state byte) bool {
	return state == 'Z' || state == 'X' || state == 'x'
}
