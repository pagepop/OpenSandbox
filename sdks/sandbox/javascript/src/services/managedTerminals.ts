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
  CreateManagedTerminalRequest,
  ManagedTerminalAttachRequest,
  ManagedTerminalConnected,
  ManagedTerminalExit,
  ManagedTerminalForeground,
  ManagedTerminalOutputEOF,
  ManagedTerminalOutputGap,
  ManagedTerminalReady,
  ManagedTerminalStatus,
  SignalManagedTerminalForegroundRequest,
  TerminateManagedTerminalRequest,
} from "../models/managedTerminal.js";

/** Raw terminal input and byte-preserving merged-output attachment. */
export interface ManagedTerminalAttachment {
  readonly connected: Promise<ManagedTerminalConnected>;
  readonly output: AsyncIterable<Uint8Array>;
  readonly gaps: AsyncIterable<ManagedTerminalOutputGap>;
  readonly outputEOF: Promise<ManagedTerminalOutputEOF>;
  readonly exit: Promise<ManagedTerminalExit>;
  readonly outputOffset: number;

  write(data: Uint8Array): Promise<void>;
  resize(rows: number, cols: number): Promise<void>;
  close(): void;
}

/** Deferred handle for one idempotent managed-terminal create attempt. */
export interface ManagedTerminalHandle {
  readonly ready: Promise<ManagedTerminalReady>;
  readonly terminalId: string | undefined;
  readonly pid: number | undefined;

  get(signal?: AbortSignal): Promise<ManagedTerminalStatus>;
  foreground(signal?: AbortSignal): Promise<ManagedTerminalForeground>;
  signalForeground(
    request: SignalManagedTerminalForegroundRequest,
    signal?: AbortSignal,
  ): Promise<number>;
  terminate(
    request?: TerminateManagedTerminalRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalStatus>;
  delete(signal?: AbortSignal): Promise<void>;
  attach(
    request: ManagedTerminalAttachRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalAttachment>;
}

/** Control-plane and I/O facade for managed terminals. */
export interface ManagedTerminals {
  create(
    request: CreateManagedTerminalRequest,
    signal?: AbortSignal,
  ): ManagedTerminalHandle;
  get(terminalId: string, signal?: AbortSignal): Promise<ManagedTerminalStatus>;
  foreground(
    terminalId: string,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalForeground>;
  signalForeground(
    terminalId: string,
    request: SignalManagedTerminalForegroundRequest,
    signal?: AbortSignal,
  ): Promise<number>;
  terminate(
    terminalId: string,
    request?: TerminateManagedTerminalRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalStatus>;
  delete(terminalId: string, signal?: AbortSignal): Promise<void>;
  attach(
    terminalId: string,
    request: ManagedTerminalAttachRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalAttachment>;
}
