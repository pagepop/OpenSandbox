# execd - OpenSandbox Execution Daemon

`execd` is the runtime daemon used inside OpenSandbox sandboxes.

It is built on Gin and exposes HTTP APIs for code execution, shell commands, filesystem operations, PTY sessions, and metrics.

## Quick Start

### 1) Build

```bash
cd components/execd
make build
```

### 2) Start Jupyter Server

```bash
./tests/jupyter.sh
```

### 3) Run execd

```bash
./bin/execd \
  --jupyter-host=http://127.0.0.1:54321 \
  --jupyter-token=your-jupyter-token \
  --port=44772
```

### 4) Verify

```bash
curl -v http://localhost:44772/ping
```

## API

- OpenAPI spec: `../../specs/execd-api.yaml`
- Common capability groups:
  - Code execution (`/code`, SSE stream)
  - Session and command execution (`/session`, `/command`)
  - Filesystem operations (`/files`, `/directories`)
  - PTY over WebSocket (`/pty`)
  - Local metrics endpoints (`/metrics`, `/metrics/watch`)

## Configuration

### CLI Flags

| Flag | Default | Description |
|---|---|---|
| `--jupyter-host` | `""` | Jupyter server URL reachable by execd. |
| `--jupyter-token` | `""` | Jupyter token for HTTP/WebSocket auth. |
| `--port` | `44772` | HTTP listen port. |
| `--log-level` | `6` | Log level (0=Emergency, 7=Debug). |
| `--access-token` | `""` | Optional shared API access token. |
| `--graceful-shutdown-timeout` | `1s` | SSE tail-drain wait window before closing. |
| `--jupyter-idle-poll-interval` | `100ms` | Poll interval after Jupyter reports idle. |

### Environment Variables

| Variable | Description |
|---|---|
| `JUPYTER_HOST` | Same as `--jupyter-host` (overridden by explicit flag). |
| `JUPYTER_TOKEN` | Same as `--jupyter-token` (overridden by explicit flag). |
| `EXECD_API_GRACE_SHUTDOWN` | Same as `--graceful-shutdown-timeout`. |
| `EXECD_JUPYTER_IDLE_POLL_INTERVAL` | Same as `--jupyter-idle-poll-interval`. |
| `EXECD_CLONE3_COMPAT` | Linux clone3 compatibility switch (see below). |
| `EXECD_LOG_FILE` | Optional log output file path; default is stdout. |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | Preferred OTLP metrics endpoint. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Fallback OTLP endpoint when metrics-specific endpoint is unset. |
| `OPENSANDBOX_ID` | Optional `sandbox_id` metric/resource attribute. |
| `OPENSANDBOX_EXECD_METRICS_EXTRA_ATTRS` | Optional extra metric attrs (`k=v,k2=v2`). |

### Isolating command memory on Linux

A runtime can opt the foreground and background `/command` paths into kernel memory limits by
giving execd a delegated cgroup v2 subtree. Set `EXECD_COMMAND_MEMORY_POOL` to the absolute
path of that subtree under `/sys/fs/cgroup` and `EXECD_COMMAND_MEMORY_LIMIT_BYTES` to the
finite per-command limit in bytes. Set both or neither.

The runtime provisions an empty command pool beneath the sandbox memory cgroup, enables
the memory controller in the pool's subtree, and leaves its `memory.oom.group` at `0`.
It keeps execd outside the pool, with access to create child cgroups and start tasks in them.
User commands must not be given write access to the delegated controls. The fixed aggregate
pool limit must be below the sandbox limit, and the per-command limit below the pool limit.
Reserve enough room outside the pool for execd and the sandbox's other services.

A command and its descendants are subject to the same limit from launch. The kernel can kill
that entire command group when it reaches its limit. A recorded OOM kill is reported as
a failed command (exit 137). Reaching a limit or getting exit 137 alone is not OOM evidence.
An invalid delegation or unavailable `clone3(CLONE_INTO_CGROUP)` rejects execution instead
of starting an unrestricted command. Do not enable `EXECD_CLONE3_COMPAT` with this configuration.
This setting does not cover the separate Jupyter, interactive shell, or PTY endpoints.

To check a delegated **test** sandbox with an outer 2 GiB limit, run the opt-in Linux smoke
test, then verify the original container's restart count and that the same sandbox can
execute another command:

```bash
EXECD_TEST_COMMAND_MEMORY_ISOLATION=1 go test ./pkg/runtime -run '^TestCommandMemoryIsolationIn2GiBSandbox$' -v
```

## Observability

### OpenTelemetry Metrics

OTLP metrics export is enabled when either endpoint is set:

- `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
- `OTEL_EXPORTER_OTLP_ENDPOINT`

Detailed metric list and attributes [opentelemetry.md](./docs/opentelemetry.md).

### Local Metrics Endpoints

- `GET /metrics`: point-in-time host metrics snapshot
- `GET /metrics/watch`: SSE stream (1s cadence)

## Linux clone3 Compatibility

Some sandbox environments fail on `clone3(2)`.  
Set `EXECD_CLONE3_COMPAT` in sandbox env to force fallback behavior:

- `1` / `true` / `yes` / `on`: enable seccomp fallback
- `reexec`: enable fallback and re-exec binary

## License

`execd` is part of OpenSandbox. See `../../LICENSE`.
