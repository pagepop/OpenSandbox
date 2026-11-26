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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandMemoryRejectsDaemonInsidePool(t *testing.T) {
	pool := "/sandbox/commands"
	require.Error(t, validateCommandMemoryMembership(pool, "0::/sandbox/commands/child\n"))
	require.Error(t, validateCommandMemoryMembership(pool, "0::/other-sandbox/service\n"))
	require.Error(t, validateCommandMemoryMembership(pool, "5:memory:/sandbox/service\n"))
	require.NoError(t, validateCommandMemoryMembership(pool, "0::/sandbox/services\n"))
}

func TestCommandMemoryOOMRequiresKernelKill(t *testing.T) {
	require.False(t, commandMemoryOOM("max 32\noom 1\noom_kill 0\n"))
	require.False(t, commandMemoryOOM("oom_kill invalid\n"))
	require.False(t, commandMemoryOOM("oom_group_kill 1\n"))
	require.True(t, commandMemoryOOM("max 32\noom 1\noom_kill 1\n"))
}

func TestParseCommandMemoryLimit(t *testing.T) {
	for _, invalid := range []string{"", "max", "0", "-1", "2Gi"} {
		_, err := parseMemoryLimit(invalid)
		require.Error(t, err, invalid)
	}
	limit, err := parseMemoryLimit("536870912\n")
	require.NoError(t, err)
	require.EqualValues(t, 512<<20, limit)
}
