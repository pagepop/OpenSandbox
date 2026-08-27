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

import type { ManagedProcessEnvironment } from "./managedProcess.js";

/** Environment patch applied over execd's scrubbed sandbox environment. */
export type ManagedTerminalEnvironment = ManagedProcessEnvironment;

/** Observable managed-terminal lifecycle states. */
export type ManagedTerminalState =
  | "allocating"
  | "running"
  | "exited"
  | "quiescent";

/** Signals supported for a terminal's current foreground process group. */
export type ManagedTerminalSignal =
  | "SIGINT"
  | "SIGTERM"
  | "SIGKILL"
  | "SIGTSTP"
  | "SIGHUP";

/** Request for one idempotent exact-argv terminal allocation. */
export interface CreateManagedTerminalRequest {
  operationId: string;
  argv: string[];
  cwd: string;
  env?: ManagedTerminalEnvironment;
  rows: number;
  cols: number;
  graceMs?: number;
}

/** Optional grace override for managed-terminal termination. */
export interface TerminateManagedTerminalRequest {
  /** Zero requests immediate SIGKILL; omission uses the create-time default. */
  graceMs?: number;
}

/** Direct outcome, retained output, and session-quiescence facts. */
export interface ManagedTerminalStatus {
  terminalId: string;
  /** Present after remote terminal publication. */
  pid?: number;
  state: ManagedTerminalState;
  exitCode: number | null;
  signal: string | null;
  topLevelExited: boolean;
  treeEmpty: boolean;
  outputOffset: number;
  outputRetainedFrom: number;
  outputEof: boolean;
}

/** Deferred publication fact exposed by a managed-terminal handle. */
export interface ManagedTerminalReady {
  pid: number;
}

/** Current terminal foreground-process facts. */
export interface ManagedTerminalForeground {
  processGroup: number;
  inputWaiting: boolean;
}

/** Request to signal the terminal's current foreground process group. */
export interface SignalManagedTerminalForegroundRequest {
  signal: ManagedTerminalSignal;
}

/** Retained output position supplied when attaching terminal I/O. */
export interface ManagedTerminalAttachRequest {
  outputOffset: number;
}

/** Initial identity and tail position published by a terminal attachment. */
export interface ManagedTerminalConnected {
  terminalId: string;
  outputOffset: number;
}

/** One unavailable output interval reported before retained replay. */
export interface ManagedTerminalOutputGap {
  requestedOffset: number;
  retainedFrom: number;
}

/** Final retained output offset. */
export interface ManagedTerminalOutputEOF {
  offset: number;
}

/** Direct-process outcome delivered on the terminal attachment. */
export interface ManagedTerminalExit {
  exitCode: number | null;
  signal: string | null;
}
