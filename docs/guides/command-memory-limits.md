---
title: Command Memory Limits
description: Configure delegated cgroup v2 memory budgets for execd foreground and background commands.
---

# Command Memory Limits

On Linux, a runtime can opt the foreground and background `/command` paths into kernel memory limits by giving execd a delegated cgroup v2 subtree. Set `EXECD_COMMAND_MEMORY_POOL` to the absolute path of that subtree under `/sys/fs/cgroup` and `EXECD_COMMAND_MEMORY_LIMIT_BYTES` to the finite per-command limit in bytes. Set both or neither. With neither setting present, command execution keeps its existing behavior.

## Runtime requirements

The runtime provisions an empty command pool beneath the sandbox memory cgroup, enables the memory controller in the pool's subtree, and leaves its `memory.oom.group` at `0`. It keeps execd outside the pool, with access to create child cgroups and start tasks in them. User commands must not be given write access to the delegated controls.

The fixed aggregate pool limit must be below the sandbox limit, and the per-command limit below the pool limit. Reserve enough room outside the pool for execd and the sandbox's other services.

A command and its descendants are subject to the same limit from launch. The kernel can kill that entire command group when it reaches its limit. A recorded OOM kill is reported as a failed command with exit code `137`. Reaching a limit or getting exit code `137` alone is not OOM evidence.

An invalid delegation or unavailable `clone3(CLONE_INTO_CGROUP)` rejects execution instead of starting an unrestricted command. Do not enable `EXECD_CLONE3_COMPAT` with this configuration. This setting does not cover the separate Jupyter, persistent Bash session, or PTY endpoints.

## Verify an isolated test sandbox

Supply a dedicated delegated cgroup in a test sandbox with an outer 2 GiB limit. Run the opt-in Linux smoke test from `components/execd`, then verify the original container's restart count and that the same sandbox can execute another command:

```bash
EXECD_TEST_COMMAND_MEMORY_ISOLATION=1 go test ./pkg/runtime -run '^TestCommandMemoryIsolationIn2GiBSandbox$' -v
```

The test allocates memory until the command reaches its budget. Run it only in the dedicated test sandbox, never against the host's cgroup hierarchy.
