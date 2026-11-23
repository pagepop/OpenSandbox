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
	"path"
	"strconv"
	"strings"
)

const (
	commandMemoryPoolEnv  = "EXECD_COMMAND_MEMORY_POOL"
	commandMemoryLimitEnv = "EXECD_COMMAND_MEMORY_LIMIT_BYTES"
)

var errCommandMemoryLimit = errors.New("command was terminated by the sandbox execution memory budget")

const commandMemoryExitCode = 137

func commandMemoryEnabled() bool {
	_, hasPool := os.LookupEnv(commandMemoryPoolEnv)
	_, hasLimit := os.LookupEnv(commandMemoryLimitEnv)
	return hasPool || hasLimit
}

func validateCommandMemoryMembership(pool, membership string) error {
	for _, line := range strings.Split(membership, "\n") {
		if current, found := strings.CutPrefix(line, "0::"); found {
			if !path.IsAbs(current) || !withinCgroup(current, path.Dir(pool)) || withinCgroup(current, pool) {
				return fmt.Errorf("execd must be inside the sandbox budget and outside the command pool")
			}
			return nil
		}
	}
	return fmt.Errorf("execd's cgroup v2 membership is unavailable")
}

func withinCgroup(item, group string) bool {
	return group == "/" || item == group || strings.HasPrefix(item, group+"/")
}

func parseMemoryLimit(value string) (uint64, error) {
	limit, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || limit == 0 {
		return 0, fmt.Errorf("missing finite positive memory limit in bytes")
	}
	return limit, nil
}

func commandMemoryOOM(events string) bool {
	for _, line := range strings.Split(events, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "oom_kill" {
			count, err := strconv.ParseUint(fields[1], 10, 64)
			return err == nil && count > 0
		}
	}
	return false
}
