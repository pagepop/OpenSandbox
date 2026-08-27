---
title: Managed Process and Terminal API
authors:
  - "PagePop Team"
creation-date: 2026-08-26
last-updated: 2026-08-27
status: implementing
---

# OSEP-0023: Managed Process and Terminal API

<!-- toc -->
- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Requirements](#requirements)
- [Proposal](#proposal)
  - [Compatibility](#compatibility)
  - [Runtime ownership](#runtime-ownership)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Process lifecycle](#process-lifecycle)
  - [API surface](#api-surface)
  - [Process request and status](#process-request-and-status)
  - [Executable resolution and environment](#executable-resolution-and-environment)
  - [I/O transport](#io-transport)
  - [Termination and quiescence](#termination-and-quiescence)
  - [Terminal sessions](#terminal-sessions)
  - [execd shutdown](#execd-shutdown)
  - [Client integration](#client-integration)
  - [Implementation tracks](#implementation-tracks)
- [Test Plan](#test-plan)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Infrastructure Needed](#infrastructure-needed)
- [Upgrade & Migration Strategy](#upgrade--migration-strategy)
<!-- /toc -->

## Summary

Add versioned `execd` APIs for exact-argv managed processes and terminal sessions. The APIs provide raw standard I/O, asynchronous process publication, direct-process exit facts, process-group termination, and observable quiescence without changing the existing `/command` and `/pty` APIs.

The initial implementation targets Linux sandboxes, including Kubernetes nodes that use cgroup v1. It uses ordinary process sessions and process groups and does not require privileged containers or writable host cgroups.

## Motivation

The existing `/command` API executes one shell string and streams line-oriented text. It cannot represent an exact argv vector, persistent stdin, byte-faithful separate stdout and stderr, direct signal outcomes, or a wait for every member of the managed process group. The existing PTY API provides useful byte transport but does not expose the complete process, environment, foreground-group, outcome, and cleanup semantics required by reusable agent runtimes.

Agent harnesses should be able to build Bash, language-server, persistent terminal, and other process consumers on one provider-neutral process service. `execd` is the correct substrate because it runs in the same sandbox execution environment as filesystem operations and can own process creation and cleanup directly.

### Goals

1. Start an executable from an exact argv vector without shell interpretation.
2. Resolve executables and apply environment changes in the sandbox execution environment.
3. Provide raw stdin, stdout, and stderr with explicit EOF and byte offsets.
4. Publish one opaque process identity and make create requests idempotent.
5. Report the direct process exit code or terminating signal separately from process-group quiescence.
6. Implement idempotent TERM-to-KILL process-group termination and wait until the group is quiescent.
7. Provide terminal allocation, input/output, foreground-group inspection and signalling, and awaited session cleanup.
8. Preserve existing command and PTY APIs.
9. Support a deferred client handle whose readiness resolves after remote process publication.

### Non-Goals

1. Sandbox creation, renewal, generation fencing, user identity, or workspace volume management.
2. Recovering process ownership after `execd` or the sandbox container restarts.
3. Adding privileged containers, host PID access, or writable host cgroup mounts.
4. Preventing a root process inside its own sandbox from deliberately disrupting `execd`.
5. Recovering a process that intentionally creates a new session outside its original process group.
6. Replacing or changing the existing `/command` and `/pty` APIs.
7. Windows support in the first implementation.

## Requirements

| ID | Requirement | Priority |
|---|---|---|
| R1 | Process start accepts a non-empty exact argv vector and an absolute working directory | Must Have |
| R2 | Executable resolution and process start use the same sandbox-side environment rules | Must Have |
| R3 | A caller operation ID identifies one create attempt and cannot produce two processes | Must Have |
| R4 | Process control uses an opaque process ID; numeric PID values are diagnostic facts only | Must Have |
| R5 | stdin, stdout, and stderr preserve arbitrary bytes and explicit EOF | Must Have |
| R6 | stdout and stderr remain separate for ordinary processes | Must Have |
| R7 | Process status distinguishes direct exit facts from process-group quiescence | Must Have |
| R8 | Termination is idempotent and performs SIGTERM, a caller-provided grace period, then SIGKILL | Must Have |
| R9 | A client can reattach to the same process and resume output from byte offsets | Must Have |
| R10 | Terminal sessions expose the active foreground process group and deliver supported signals to it | Must Have |
| R11 | `execd` shutdown does not leave managed work running in a live sandbox container | Must Have |
| R12 | Public OpenAPI and affected SDKs remain aligned with the implementation | Must Have |

## Proposal

`execd` gains two related managers behind additive `/v1` routes:

```text
                         execd
                           |
              +------------+-------------+
              |                          |
      ManagedProcessManager       ManagedTerminalManager
              |                          |
      exact argv + raw pipes       PTY + controlling tty
              |                          |
      process session/group       foreground process group
```

Both managers publish opaque IDs, retain lifecycle state in memory, and own cleanup. Filesystem paths, sandbox endpoint authorization, and sandbox generation selection remain outside these managers.

### Compatibility

The new API is additive. Existing `/command`, `/command/status`, `/command/{id}/logs`, and `/pty` behavior remains unchanged. Existing SDK methods continue to use those routes until callers opt in to the managed process API.

### Runtime ownership

Each process belongs to exactly one running `execd` instance. The registry is not persisted. If `execd` exits, the sandbox bootstrap exits and lets the container runtime stop the remaining processes and restart or replace the sandbox.

Every create request carries a caller-generated `operationId`. Repeating the same operation with the same request returns the original handle while its resource record exists. Reusing the operation ID with different request fields, or retrying it after the resource record is deleted, returns `409 Conflict`.

### Notes/Constraints/Caveats

- The validated Tencent TKE environment runs Kubernetes `v1.20.6-tke.21`, Linux `3.10`, and cgroup v1. Sandbox containers do not receive writable cgroup controllers by default.
- Process containment therefore follows the same detached process-group model as the current Linux harness provider: one new session/group per managed process, group signalling, and `/proc` liveness inspection that ignores zombie-only groups.
- The current PagePop sandbox image runs as root. The first implementation keeps this behavior for workload compatibility. Management records stay in `execd` memory; output retention uses an execd-owned ephemeral directory and carries no credentials.
- A transport loss never starts another process. A client may reattach only to the same opaque ID in the same sandbox instance.

### Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Lost create response causes a duplicate process | Require `operationId`; return the existing process for an identical retry |
| Numeric PID or process-group ID is reused | Keep numeric identities private, record Linux start time, and stop signalling after quiescence is proven |
| A child holds output descriptors after the direct process exits | Publish direct exit immediately, keep output draining while the managed group remains live, and keep group liveness independent |
| A transport disconnect loses output | Retain bounded output and resume by per-stream byte offset; report a gap rather than invent bytes |
| Graceful termination fails | Escalate to SIGKILL and keep `treeEmpty=false` until liveness inspection proves quiescence |
| `execd` crashes while work is running | Make `execd` a critical bootstrap child so the sandbox container exits instead of continuing unmanaged |
| Same-sandbox root code tampers with the daemon | Accept as an initial sandbox-local limitation; do not place credentials or authoritative identities in user-writable files |

## Design Details

### Process lifecycle

The manager uses a small state model:

```text
allocating -> running -> exited
     |           |          |
     +-----------+----------+-> quiescent
```

- `allocating` begins after the operation ID is reserved.
- `running` begins only after `exec.Cmd.Start` succeeds and identity facts are recorded.
- `exited` records the direct process wait result.
- `quiescent` is an independent fact proving the process group has no executing member.
- A spawn failure before publication removes the operation reservation and returns an error.

### API surface

The OpenAPI specification defines these additive routes:

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/processes/resolve-executable` | Resolve and validate one executable using managed-process environment rules |
| `POST` | `/v1/processes` | Idempotently create one managed process |
| `GET` | `/v1/processes/{processId}` | Read publication, direct outcome, and quiescence facts |
| `GET` | `/v1/processes/{processId}/io` | Attach a bidirectional WebSocket for stdin/stdout/stderr |
| `POST` | `/v1/processes/{processId}/terminate` | Start or join TERM-to-KILL termination |
| `DELETE` | `/v1/processes/{processId}` | Remove a quiescent process record and retained output |
| `POST` | `/v1/terminals` | Idempotently allocate a terminal and start exact argv |
| `GET` | `/v1/terminals/{terminalId}` | Read terminal publication and direct outcome |
| `GET` | `/v1/terminals/{terminalId}/io` | Attach terminal input/output WebSocket transport |
| `GET` | `/v1/terminals/{terminalId}/foreground` | Inspect the current foreground process group |
| `POST` | `/v1/terminals/{terminalId}/foreground/signal` | Signal the current foreground process group |
| `POST` | `/v1/terminals/{terminalId}/terminate` | Terminate and await the complete terminal session |
| `DELETE` | `/v1/terminals/{terminalId}` | Remove a terminated terminal record and retained output |

All control routes are idempotent where repeating an identical request has a defined result. Unknown or already-removed opaque resource IDs return `404`; a deleted create operation remains tombstoned and returns `409` if its `operationId` is retried.

### Process request and status

The process create request contains:

```json
{
  "operationId": "caller-generated-opaque-id",
  "argv": ["/usr/bin/node", "script.js"],
  "cwd": "/workspace",
  "env": {
    "LANG": "C.UTF-8",
    "REMOVED_NAME": null
  },
  "stdin": "pipe",
  "stdoutRetentionBytes": 1048576,
  "stderrRetentionBytes": 1048576,
  "graceMs": 3000
}
```

`env` is a patch over a scrubbed sandbox-side base. A string sets a value and `null` removes it. The same resolver builds the environment for executable lookup and process start.

The status response contains at least:

```json
{
  "processId": "opaque-process-id",
  "pid": 1234,
  "state": "running",
  "exitCode": null,
  "signal": null,
  "topLevelExited": false,
  "treeEmpty": false,
  "stdoutOffset": 0,
  "stderrOffset": 0,
  "stdoutRetainedFrom": 0,
  "stderrRetainedFrom": 0,
  "stdoutSpillPath": null,
  "stderrSpillPath": null
}
```

`pid` is present only after publication. Exactly one of `exitCode` and `signal` is present after a direct-process exit. `treeEmpty` does not become true merely because the direct process exited. A spill path is published only after its stream reaches clean EOF and the complete stream fits within the requested retention limit.

### Executable resolution and environment

- Absolute executable paths must identify an executable regular file.
- Bare names resolve against the effective `PATH` in the sandbox.
- Relative values containing `/` are rejected.
- Resolution returns one absolute path.
- The environment base removes internal daemon variables, `DSH_*`, and credential-shaped names before applying explicit caller values.
- argv and environment values are passed directly to `execve` through Go `os/exec`; no shell command is constructed.

### I/O transport

Ordinary processes use one bidirectional WebSocket per attachment.

- Binary client frames carry stdin bytes with a monotonically increasing sequence number.
- A control frame closes stdin explicitly.
- Binary server frames identify stdout or stderr and carry the stream's starting byte offset.
- Server control frames acknowledge stdin, publish stream EOF, report output gaps, and publish the direct-process outcome.
- Reattachment supplies the last accepted stdin sequence and desired stdout/stderr offsets.
- Duplicate acknowledged stdin frames are ignored; unacknowledged frames may be resent.
- Retention is bounded by the create request. An unavailable offset produces an explicit gap response.
- Only one attachment may write stdin at a time. A newer authenticated attachment replaces the prior attachment.

Retained files live under `/tmp/opensandbox-execd/processes/{processId}/` in the sandbox filesystem. They contain no request environment or credentials and are removed with the process record. The SDK maps the transport to language-native streams. Pipe, collect, and inherit behavior remains a client concern; `execd` supplies raw bytes and retained paths/offsets.

### Termination and quiescence

Each ordinary process starts in a new POSIX session and process group. The manager records its PID, process-group ID, and Linux `/proc/{pid}/stat` start time before publication.

Termination performs:

1. Signal the recorded group with SIGTERM.
2. Wait up to `graceMs` for no non-zombie group member.
3. Signal the group with SIGKILL if it remains live.
4. Continue observing until the group is quiescent or the caller stops waiting.

The terminate request joins an existing termination attempt. It never redirects a signal to another process or another sandbox. Once absence is proven, the record permanently stops issuing signals.

### Terminal sessions

The managed terminal implementation extends the existing PTY runtime rather than introducing another PTY library.

- Create accepts exact argv, cwd, environment patch, initial rows/columns, operation ID, and grace period.
- The child owns a controlling terminal and a new session.
- Input and output are raw terminal bytes; stdout and stderr have normal PTY merged-stream semantics.
- Foreground inspection uses `tcgetpgrp`. `inputWaiting` is true only when `execd` can prove the foreground group is blocked on terminal input; inability to prove it returns false.
- Supported foreground signals are SIGINT, SIGTERM, SIGKILL, SIGTSTP, and SIGHUP.
- Terminal termination rejects new operations, joins in-flight writes/inspection/signals, terminates all observable session groups, drains queued output, and returns only after quiescence.

### execd shutdown

The bootstrap records the `execd` PID and treats daemon exit as fatal. If `execd` exits while the workload entrypoint remains active, bootstrap forwards termination to the workload and exits non-zero so the container runtime removes any remaining managed processes.

Normal `execd` shutdown rejects new allocations, starts force-bounded cleanup for managed processes and terminals, then stops the HTTP server. If cleanup cannot prove quiescence within the shutdown bound, `execd` exits non-zero so container teardown remains the final cleanup boundary.

### Client integration

The JavaScript and Go sandbox SDKs receive handwritten managed-process facades over the generated API and WebSocket transport. Other generated SDKs remain schema-aligned; ergonomic streaming facades may follow without blocking the PagePop integration.

Remote synchronous callers use a deferred handle:

```ts
interface RemoteProcessHandle {
  readonly ready: Promise<{ pid: number }>
  readonly pid: number | undefined
}
```

The handle exposes streams immediately. stdin waits for readiness. Consumers that need a numeric PID await `ready`. This replaces the ambiguous E2B proof-of-concept behavior where `pid === -1` means either startup is pending or startup failed.

The DeepSeek Harness `ctx.subprocess` adapter is a downstream consumer and is not implemented in this repository. It binds each handle to the sandbox instance selected by its lifecycle owner; `execd` does not accept user IDs or sandbox generation overrides.

### Implementation tracks

The work can proceed in three parallel tracks after the request and response models are frozen:

1. **Managed process runtime**: registry, operation idempotency, exact argv, environment, raw pipes, outcome, process-group termination, shutdown, and runtime tests.
2. **Wire and clients**: OpenAPI schemas, Gin controllers/routes, WebSocket framing, JavaScript/Go SDK facades, and protocol tests.
3. **Managed terminal**: exact-argv PTY allocation, foreground inspection/signalling, terminal termination, and PTY tests.

Integration review verifies that the runtime manager remains independent of Gin and SDK types, controllers do not own lifecycle state, and ordinary process and terminal cleanup use the same identity and quiescence rules.

## Test Plan

Unit and component tests cover:

1. Exact argv preserves spaces, quotes, shell metacharacters, empty arguments, and Unicode without execution by a shell.
2. Environment set/remove behavior and executable resolution use the same effective PATH.
3. Duplicate operation IDs return one process; conflicting reuse returns `409`.
4. Arbitrary binary output and CR/LF sequences survive fragmented WebSocket frames with separate stdout/stderr offsets.
5. Streaming stdin, explicit EOF, acknowledgement, and same-handle reattachment do not duplicate bytes.
6. Exit code and signal outcomes are reported accurately.
7. A TERM-trapping process escalates to SIGKILL after the requested grace period.
8. A direct child exit with a surviving group member leaves `treeEmpty=false` until the survivor exits or is terminated.
9. Repeated and late terminate calls do not signal a reused PID or process group.
10. Allocation cancellation before and after process publication leaves no unmanaged process.
11. `execd` shutdown joins every managed process and terminal or causes the sandbox container to exit.
12. PTY tests cover initial dimensions, raw input/output, foreground-group inspection, supported signals, queued-output drain, and awaited cleanup.
13. A TKE smoke test runs Bash and a raw-pipe language server in a real sandbox without privileged or writable-cgroup configuration.

## Drawbacks

- The API adds a second process execution surface alongside `/command`.
- Byte-faithful reattachment requires bounded output retention and a WebSocket protocol not generated by ordinary OpenAPI clients.
- Process-group containment cannot observe a process that deliberately leaves its session; stronger containment requires a different runtime substrate.
- Running workload processes as root does not protect `execd` from deliberate same-sandbox interference.

## Alternatives

### Adapt `/command`

Rejected because shell text, line-oriented SSE, merged background output, missing stdin, and incomplete status semantics would require breaking existing clients or preserving two incompatible modes on one route.

### Copy the E2B shell wrapper

Rejected as the final implementation. The wrapper demonstrates deferred publication and cleanup but relies on quoted shell programs, same-UID state files, base64 framing, and control-shell polling. Native `execd` process ownership removes those workarounds.

### Require cgroup v2

Rejected for the initial implementation because the deployed TKE nodes use cgroup v1 and expose cgroup controllers read-only inside ordinary sandbox containers.

### Run privileged for cgroup v1 delegation

Rejected because host cgroup access materially weakens sandbox isolation for a capability that process groups can provide without elevated privileges.

### Persist and recover process registries

Rejected because sandbox/container restart already invalidates the execution instance. Failing the container with `execd` is simpler and prevents uncertain process ownership.

## Infrastructure Needed

No new service or Kubernetes privilege is required. The implementation uses the existing `execd` binary, sandbox endpoint, filesystem, Linux process APIs, and WebSocket support.

The TKE deployment should pin the OpenSandbox controller and `execd` images by immutable tag or digest before end-to-end validation. The currently observed controller image uses the mutable `:tag` label.

## Upgrade & Migration Strategy

The change is additive. Deploy a new `execd` image and SDK version, then enable managed-process consumers. Existing command and PTY SDK methods continue to work unchanged.

The PagePop rollout sequence is:

1. Deploy the new `execd` API and verify it through JavaScript and Go SDK smoke tests.
2. Add a CloudSubprocess provider that exposes deferred readiness and binds the process to one sandbox instance.
3. Enable Bash, file search, and LSP consumers.
4. Enable terminal consumers after managed PTY tests pass.
5. Remove any temporary provider-specific command path only after equivalent product behavior is verified.
