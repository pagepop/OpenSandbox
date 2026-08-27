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

import type {
  CreateManagedProcessRequest,
  ManagedProcessAttachRequest,
  ManagedProcessConnected,
  ManagedProcessExit,
  ManagedProcessOutputGap,
  ManagedProcessReady,
  ManagedProcessStatus,
  ManagedProcessStreamEOF,
  ResolveExecutableRequest,
  ResolveExecutableResponse,
  TerminateManagedProcessRequest,
} from "../models/managedProcess.js";

/** Raw, acknowledged stdin and byte-preserving stdout/stderr attachment. */
export interface ManagedProcessAttachment {
  readonly connected: Promise<ManagedProcessConnected>;
  readonly stdout: AsyncIterable<Uint8Array>;
  readonly stderr: AsyncIterable<Uint8Array>;
  readonly gaps: AsyncIterable<ManagedProcessOutputGap>;
  readonly stdoutEOF: Promise<ManagedProcessStreamEOF>;
  readonly stderrEOF: Promise<ManagedProcessStreamEOF>;
  readonly exit: Promise<ManagedProcessExit>;
  readonly stdinSequence: number;
  readonly stdoutOffset: number;
  readonly stderrOffset: number;

  /** Sends one stdin frame and resolves when execd acknowledges its sequence. */
  write(sequence: number, data: Uint8Array): Promise<void>;
  /** Sends explicit stdin EOF and resolves when execd accepts it. */
  closeStdin(sequence: number): Promise<void>;
  close(): void;
}

/** Deferred handle for one idempotent managed-process create attempt. */
export interface ManagedProcessHandle {
  /** Resolves after execd publishes the remote process, or rejects on create failure. */
  readonly ready: Promise<ManagedProcessReady>;
  /** Opaque process identity, unavailable until `ready` resolves. */
  readonly processId: string | undefined;
  /** Numeric diagnostic PID, unavailable until `ready` resolves. */
  readonly pid: number | undefined;

  get(signal?: AbortSignal): Promise<ManagedProcessStatus>;
  terminate(
    request?: TerminateManagedProcessRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessStatus>;
  delete(signal?: AbortSignal): Promise<void>;
  attach(
    request: ManagedProcessAttachRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessAttachment>;
}

/** Control-plane facade for OSEP-0023 managed processes. */
export interface ManagedProcesses {
  resolveExecutable(
    request: ResolveExecutableRequest,
    signal?: AbortSignal,
  ): Promise<ResolveExecutableResponse>;

  /** Starts the create request immediately and returns before remote publication. */
  create(
    request: CreateManagedProcessRequest,
    signal?: AbortSignal,
  ): ManagedProcessHandle;

  get(processId: string, signal?: AbortSignal): Promise<ManagedProcessStatus>;
  terminate(
    processId: string,
    request?: TerminateManagedProcessRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessStatus>;
  delete(processId: string, signal?: AbortSignal): Promise<void>;
  attach(
    processId: string,
    request: ManagedProcessAttachRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessAttachment>;
}
