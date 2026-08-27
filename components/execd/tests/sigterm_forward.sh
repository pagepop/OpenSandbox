#!/bin/bash
# Copyright 2026 Alibaba Group Holding Ltd.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Test: bootstrap.sh forwards K8s SIGTERM to user process.
#
# Simulates K8s termination: send SIGTERM to bootstrap process,
# verify user process receives it via trap marker file.
#
# Usage:
#   cd components/execd
#   bash tests/sigterm_forward.sh

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BOOTSTRAP="$ROOT_DIR/bootstrap.sh"

TESTDIR="$(mktemp -d)"
MARKER_STARTED="$TESTDIR/started"
MARKER_SIGTERM="$TESTDIR/sigterm_received"
CMD_PID_FILE="$TESTDIR/cmd_pid"
EXECD_STARTED="$TESTDIR/execd_started"
EXECD_PID_FILE="$TESTDIR/execd_pid"

cleanup() {
    for pid in "${BOOTSTRAP_PID:-}" "${FATAL_BOOTSTRAP_PID:-}"; do
        [ -n "$pid" ] && kill -KILL "$pid" 2>/dev/null || true
    done
    for pid_file in "$CMD_PID_FILE" "$EXECD_PID_FILE" "$TESTDIR"/fatal_workload_pid_*; do
        if [ -f "$pid_file" ]; then
            kill -KILL "$(cat "$pid_file")" 2>/dev/null || true
        fi
    done
    rm -rf "$TESTDIR"
}
trap cleanup EXIT

wait_for_file() {
    file="$1"
    description="$2"
    for _ in $(seq 1 100); do
        [ -f "$file" ] && return 0
        sleep 0.05
    done
    echo "FAIL: $description did not occur within 5s"
    exit 1
}

wait_for_exit() {
    pid="$1"
    description="$2"
    for _ in $(seq 1 100); do
        ! kill -0 "$pid" 2>/dev/null && return 0
        sleep 0.05
    done
    echo "FAIL: $description did not exit within 5s"
    exit 1
}

# Write test helper: traps SIGTERM, writes marker, then exits.
# Must use shell builtin loop so bash stays alive as PID and trap can fire.
# Without this, bash -c exec's child process and trap handler is lost.
HELPER="$TESTDIR/sigterm_helper.sh"
cat > "$HELPER" << 'HELPER_SCRIPT'
#!/bin/bash
MARKER_STARTED="$1"
MARKER_SIGTERM="$2"
PID_FILE="$3"
trap 'touch "$MARKER_SIGTERM"; exit 0' TERM
echo $$ > "$PID_FILE"
touch "$MARKER_STARTED"
while true; do
    sleep 1
done
HELPER_SCRIPT
chmod +x "$HELPER"

EXECD_HELPER="$TESTDIR/execd_helper.sh"
cat > "$EXECD_HELPER" << 'EXECD_HELPER_SCRIPT'
#!/bin/bash
trap 'exit 0' TERM
echo $$ > "$TEST_EXECD_PID_FILE"
touch "$TEST_EXECD_STARTED"
while true; do
    sleep 1
done
EXECD_HELPER_SCRIPT
chmod +x "$EXECD_HELPER"

echo "=== Test: SIGTERM forwarding from bootstrap to user process ==="

# Start bootstrap with helper as user process
EXECD="$EXECD_HELPER" \
TEST_EXECD_STARTED="$EXECD_STARTED" \
TEST_EXECD_PID_FILE="$EXECD_PID_FILE" \
BOOTSTRAP_SHELL="$(command -v bash)" \
BOOTSTRAP_CMD="$HELPER $MARKER_STARTED $MARKER_SIGTERM $CMD_PID_FILE" \
bash "$BOOTSTRAP" &
BOOTSTRAP_PID=$!

wait_for_file "$MARKER_STARTED" "user process startup"
wait_for_file "$EXECD_STARTED" "execd startup"
echo "OK: user process started (PID: $BOOTSTRAP_PID tree)"

# Simulate K8s: send SIGTERM to bootstrap process
echo "Sending SIGTERM to bootstrap PID $BOOTSTRAP_PID ..."
kill -TERM "$BOOTSTRAP_PID"

wait_for_file "$MARKER_SIGTERM" "user process SIGTERM handler"
wait_for_exit "$BOOTSTRAP_PID" "bootstrap after SIGTERM"
wait "$BOOTSTRAP_PID"
BOOTSTRAP_PID=""
rm -f "$CMD_PID_FILE" "$EXECD_PID_FILE"

# Verify user process received SIGTERM
if [ -f "$MARKER_SIGTERM" ]; then
    echo "PASS: user process received SIGTERM from bootstrap"
else
    echo "FAIL: user process did NOT receive SIGTERM"
    echo "  Bootstrap PID: $BOOTSTRAP_PID (still running: $(kill -0 "$BOOTSTRAP_PID" 2>/dev/null && echo yes || echo no))"
    echo "  Process tree:"
    pgrep -P "$BOOTSTRAP_PID" 2>/dev/null | while read -r pid; do
        echo "    child PID $pid: $(ps -p "$pid" -o comm= 2>/dev/null || echo dead)"
        pgrep -P "$pid" 2>/dev/null | while read -r child; do
            echo "      grandchild PID $child: $(ps -p "$child" -o comm= 2>/dev/null || echo dead)"
        done
    done
    exit 1
fi

echo "=== Test: fatal execd exit does not wait for TERM-ignoring workload ==="

IGNORE_TERM_HELPER="$TESTDIR/ignore_term_helper.sh"
cat > "$IGNORE_TERM_HELPER" << 'IGNORE_TERM_HELPER_SCRIPT'
#!/bin/bash
trap '' TERM
echo $$ > "$1"
touch "$2"
while true; do
    sleep 1
done
IGNORE_TERM_HELPER_SCRIPT
chmod +x "$IGNORE_TERM_HELPER"

FATAL_EXECD_HELPER="$TESTDIR/fatal_execd_helper.sh"
cat > "$FATAL_EXECD_HELPER" << 'FATAL_EXECD_HELPER_SCRIPT'
#!/bin/bash
while [ ! -f "$FATAL_WORKLOAD_STARTED" ]; do
    sleep 0.01
done
exit "$FATAL_EXECD_STATUS"
FATAL_EXECD_HELPER_SCRIPT
chmod +x "$FATAL_EXECD_HELPER"

run_fatal_execd_case() {
    local execd_status="$1"
    local expected_status="$2"
    local name="$3"
    local workload_started="$TESTDIR/fatal_workload_started_$name"
    local workload_pid_file="$TESTDIR/fatal_workload_pid_$name"

    EXECD="$FATAL_EXECD_HELPER" \
    FATAL_EXECD_STATUS="$execd_status" \
    FATAL_WORKLOAD_STARTED="$workload_started" \
    BOOTSTRAP_SHELL="$(command -v bash)" \
    BOOTSTRAP_CMD="$IGNORE_TERM_HELPER $workload_pid_file $workload_started" \
    bash "$BOOTSTRAP" &
    FATAL_BOOTSTRAP_PID=$!

    wait_for_file "$workload_started" "TERM-ignoring workload startup"
    wait_for_exit "$FATAL_BOOTSTRAP_PID" "bootstrap after fatal execd exit"
    set +e
    wait "$FATAL_BOOTSTRAP_PID"
    local bootstrap_status=$?
    set -e
    FATAL_BOOTSTRAP_PID=""

    if [ "$bootstrap_status" -ne "$expected_status" ]; then
        echo "FAIL: expected bootstrap status $expected_status for execd status $execd_status, got $bootstrap_status"
        exit 1
    fi
    local workload_pid
    workload_pid="$(cat "$workload_pid_file")"
    if ! kill -0 "$workload_pid" 2>/dev/null; then
        echo "FAIL: TERM-ignoring workload exited before bootstrap"
        exit 1
    fi
    kill -KILL "$workload_pid" 2>/dev/null || true
    rm -f "$workload_pid_file"
    echo "PASS: execd status $execd_status became bootstrap status $expected_status without waiting for the workload"
}

run_fatal_execd_case 0 1 clean
run_fatal_execd_case 42 42 nonzero
