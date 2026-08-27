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

/** Environment patch applied over execd's scrubbed sandbox environment. */
export type ManagedProcessEnvironment = Record<string, string | null>;

/** Managed-process stdin allocation supported by OSEP-0023. */
export type ManagedProcessStdinMode = "pipe";

/** Observable managed-process lifecycle states. */
export type ManagedProcessState =
  | "allocating"
  | "running"
  | "exited"
  | "quiescent";

/** Request for sandbox-side executable resolution. */
export interface ResolveExecutableRequest {
  executable: string;
  env?: ManagedProcessEnvironment;
}

/** Result of sandbox-side executable resolution. */
export interface ResolveExecutableResponse {
  path: string;
}

/** Request for one idempotent exact-argv managed-process create attempt. */
export interface CreateManagedProcessRequest {
  operationId: string;
  argv: string[];
  cwd: string;
  env?: ManagedProcessEnvironment;
  stdin: ManagedProcessStdinMode;
  stdoutRetentionBytes?: number;
  stderrRetentionBytes?: number;
  graceMs?: number;
}

/** Optional grace override for managed-process termination. */
export interface TerminateManagedProcessRequest {
  /** Zero requests immediate SIGKILL; omission uses the create-time default. */
  graceMs?: number;
}

/** Publication, direct-process outcome, output retention, and quiescence facts. */
export interface ManagedProcessStatus {
  processId: string;
  /** Present after remote process publication. */
  pid?: number;
  state: ManagedProcessState;
  exitCode: number | null;
  signal: string | null;
  topLevelExited: boolean;
  treeEmpty: boolean;
  stdinSequence: number;
  stdoutOffset: number;
  stderrOffset: number;
  stdoutRetainedFrom: number;
  stderrRetainedFrom: number;
  stdoutSpillPath: string | null;
  stderrSpillPath: string | null;
}

/** Deferred publication fact exposed by a managed-process handle. */
export interface ManagedProcessReady {
  pid: number;
}

/** Retained positions supplied when opening a managed-process I/O attachment. */
export interface ManagedProcessAttachRequest {
  stdinSequence: number;
  stdoutOffset: number;
  stderrOffset: number;
}

/** Initial process and stream positions published by an I/O attachment. */
export interface ManagedProcessConnected {
  processId: string;
  stdinSequence: number;
  stdoutOffset: number;
  stderrOffset: number;
}

/** One unavailable output interval reported before retained replay. */
export interface ManagedProcessOutputGap {
  stream: "stdout" | "stderr";
  requestedOffset: number;
  retainedFrom: number;
}

/** Final retained offset and clean-drain fact for one output stream. */
export interface ManagedProcessStreamEOF {
  offset: number;
  clean: boolean;
}

/** Direct-process outcome delivered on the I/O attachment. */
export interface ManagedProcessExit {
  exitCode: number | null;
  signal: string | null;
}
