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
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
)

func configureManagedProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

func managedProcessStartIdentity(pid int) (uint64, error) {
	if goruntime.GOOS != "linux" {
		return 0, nil
	}
	stat, err := readManagedProcStat(pid)
	if err != nil {
		return 0, err
	}
	return stat.startTime, nil
}

func managedProcessGroupLive(pid int, startTime uint64) (bool, error) {
	if goruntime.GOOS != "linux" {
		err := syscall.Kill(-pid, 0)
		switch {
		case err == nil, errors.Is(err, syscall.EPERM):
			return true, nil
		case errors.Is(err, syscall.ESRCH):
			return false, nil
		default:
			return false, err
		}
	}

	leader, err := readManagedProcStat(pid)
	if err == nil && leader.startTime != startTime {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		memberPID, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := readManagedProcStat(memberPID)
		if err != nil {
			continue
		}
		if stat.processGroup == pid && stat.state != 'Z' && stat.state != 'X' && stat.state != 'x' {
			return true, nil
		}
	}
	return false, nil
}

func signalManagedProcessGroup(pid int, startTime uint64, signal managedProcessSignal) error {
	live, err := managedProcessGroupLive(pid, startTime)
	if err != nil {
		return err
	}
	if !live {
		return os.ErrProcessDone
	}
	var unixSignal syscall.Signal
	switch signal {
	case managedProcessSignalTerm:
		unixSignal = syscall.SIGTERM
	case managedProcessSignalKill:
		unixSignal = syscall.SIGKILL
	default:
		return errors.New("unknown managed process signal")
	}
	if err := syscall.Kill(-pid, unixSignal); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

func forceManagedProcessGroup(pid int, _ uint64) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func managedProcessExitOutcome(state *os.ProcessState, waitErr error) (*int, *string) {
	if state != nil {
		if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			name := managedSignalName(status.Signal())
			return nil, &name
		}
		code := state.ExitCode()
		return &code, nil
	}
	code := 1
	if waitErr == nil {
		code = 0
	}
	return &code, nil
}

func managedSignalName(signal syscall.Signal) string {
	switch signal {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return fmt.Sprintf("SIG%d", signal)
	}
}

type managedProcStat struct {
	state        byte
	processGroup int
	session      int
	startTime    uint64
}

func readManagedProcStat(pid int) (managedProcStat, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return managedProcStat{}, err
	}
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return managedProcStat{}, errors.New("invalid /proc process stat")
	}
	fields := strings.Fields(string(data[end+2:]))
	if len(fields) <= 19 || len(fields[0]) != 1 {
		return managedProcStat{}, errors.New("incomplete /proc process stat")
	}
	processGroup, err := strconv.Atoi(fields[2])
	if err != nil {
		return managedProcStat{}, fmt.Errorf("parse /proc process group: %w", err)
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return managedProcStat{}, fmt.Errorf("parse /proc process session: %w", err)
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return managedProcStat{}, fmt.Errorf("parse /proc process start time: %w", err)
	}
	return managedProcStat{state: fields[0][0], processGroup: processGroup, session: session, startTime: startTime}, nil
}
