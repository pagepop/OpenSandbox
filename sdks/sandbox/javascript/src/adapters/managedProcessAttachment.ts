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

import type WebSocket from "ws";
import type { RawData } from "ws";

import type {
  ManagedProcessAttachRequest,
  ManagedProcessConnected,
  ManagedProcessExit,
  ManagedProcessOutputGap,
  ManagedProcessStreamEOF,
} from "../models/managedProcess.js";
import type { ManagedProcessAttachment } from "../services/managedProcesses.js";
import {
  AsyncQueue,
  deferred,
  rawDataBytes,
  type Deferred,
  WEB_SOCKET_OUTPUT_HIGH_WATER_BYTES,
  WEB_SOCKET_OUTPUT_LOW_WATER_BYTES,
} from "./webSocketQueue.js";

const STDIN_FRAME = 0x00;
const STDOUT_FRAME = 0x01;
const STDERR_FRAME = 0x02;
const NORMAL_CLOSE = 1000;
const POLICY_VIOLATION_CLOSE = 1008;

function managedProcessWebSocketUrl(
  baseUrl: string,
  processId: string,
  request: ManagedProcessAttachRequest,
): string {
  const base = baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`;
  const endpoint = new URL(
    `v1/processes/${encodeURIComponent(processId)}/io`,
    base,
  );
  if (endpoint.protocol === "http:") endpoint.protocol = "ws:";
  else if (endpoint.protocol === "https:") endpoint.protocol = "wss:";
  else throw new Error(`Unsupported execd URL scheme: ${endpoint.protocol}`);
  endpoint.searchParams.set("stdinSequence", String(request.stdinSequence));
  endpoint.searchParams.set("stdoutOffset", String(request.stdoutOffset));
  endpoint.searchParams.set("stderrOffset", String(request.stderrOffset));
  return endpoint.toString();
}

function frameNumber(value: unknown, field: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    throw new Error(`Managed process control frame has invalid ${field}`);
  }
  return value;
}

function frameString(value: unknown, field: string): string {
  if (typeof value !== "string") {
    throw new Error(`Managed process control frame has invalid ${field}`);
  }
  return value;
}

class ManagedProcessAttachmentImpl implements ManagedProcessAttachment {
  readonly stdout: AsyncQueue<Uint8Array>;
  readonly stderr: AsyncQueue<Uint8Array>;
  readonly gaps = new AsyncQueue<ManagedProcessOutputGap>();

  private readonly connectedResult = deferred<ManagedProcessConnected>();
  private readonly stdoutEOFResult = deferred<ManagedProcessStreamEOF>();
  private readonly stderrEOFResult = deferred<ManagedProcessStreamEOF>();
  private readonly exitResult = deferred<ManagedProcessExit>();
  private readonly pendingWrites = new Map<number, Deferred<void>>();
  private stdinEOF: { sequence: number; result: Deferred<void> } | undefined;
  private failed = false;
  private receivePaused = false;
  private abortListener: (() => void) | undefined;
  private currentStdinSequence: number;
  private currentStdoutOffset: number;
  private currentStderrOffset: number;

  readonly connected = this.connectedResult.promise;
  readonly stdoutEOF = this.stdoutEOFResult.promise;
  readonly stderrEOF = this.stderrEOFResult.promise;
  readonly exit = this.exitResult.promise;

  constructor(
    private readonly socket: WebSocket,
    request: ManagedProcessAttachRequest,
    private readonly signal?: AbortSignal,
  ) {
    const outputQueueChanged = () => this.updateReceiveFlow();
    this.stdout = new AsyncQueue((data) => data.byteLength, outputQueueChanged);
    this.stderr = new AsyncQueue((data) => data.byteLength, outputQueueChanged);
    this.currentStdinSequence = request.stdinSequence;
    this.currentStdoutOffset = request.stdoutOffset;
    this.currentStderrOffset = request.stderrOffset;

    for (const promise of [
      this.connected,
      this.stdoutEOF,
      this.stderrEOF,
      this.exit,
    ]) {
      void promise.catch(() => undefined);
    }

    socket.on("message", (data, isBinary) => {
      try {
        this.receive(data, isBinary);
      } catch (error) {
        this.protocolFailure(error);
      }
    });
    socket.once("error", (error) => this.fail(error));
    socket.once("close", (code, reason) => {
      const detail = reason.length === 0 ? "" : `: ${reason.toString()}`;
      const error = new Error(`Managed process attachment closed (${code})${detail}`);
      this.fail(error, code === NORMAL_CLOSE);
      if (this.signal && this.abortListener) {
        this.signal.removeEventListener("abort", this.abortListener);
      }
    });

    if (signal) {
      this.abortListener = () => {
        const reason = signal.reason instanceof Error
          ? signal.reason
          : new Error("Managed process attachment aborted");
        this.fail(reason);
        this.closeSocket(NORMAL_CLOSE, "aborted");
      };
      if (signal.aborted) this.abortListener();
      else signal.addEventListener("abort", this.abortListener, { once: true });
    }
  }

  get stdinSequence(): number {
    return this.currentStdinSequence;
  }

  get stdoutOffset(): number {
    return this.currentStdoutOffset;
  }

  get stderrOffset(): number {
    return this.currentStderrOffset;
  }

  async write(sequence: number, data: Uint8Array): Promise<void> {
    await this.connected;
    const existing = this.pendingWrites.get(sequence);
    if (existing) return existing.promise;
    if (this.stdinEOF) throw new Error("Managed process stdin is closed");
    frameNumber(sequence, "stdin sequence");

    const result = deferred<void>();
    void result.promise.catch(() => undefined);
    this.pendingWrites.set(sequence, result);
    const frame = new Uint8Array(9 + data.byteLength);
    frame[0] = STDIN_FRAME;
    new DataView(frame.buffer).setBigUint64(1, BigInt(sequence), false);
    frame.set(data, 9);
    try {
      this.socket.send(frame, (error) => {
        if (!error) return;
        this.pendingWrites.delete(sequence);
        result.reject(error);
      });
    } catch (error) {
      this.pendingWrites.delete(sequence);
      result.reject(error);
    }
    return result.promise;
  }

  async closeStdin(sequence: number): Promise<void> {
    await this.connected;
    if (this.stdinEOF) {
      if (this.stdinEOF.sequence !== sequence) {
        throw new Error("Managed process stdin EOF already used a different sequence");
      }
      return this.stdinEOF.result.promise;
    }
    frameNumber(sequence, "stdin EOF sequence");
    const result = deferred<void>();
    void result.promise.catch(() => undefined);
    this.stdinEOF = { sequence, result };
    try {
      this.socket.send(JSON.stringify({ type: "stdin_eof", sequence }), (error) => {
        if (error) result.reject(error);
      });
    } catch (error) {
      result.reject(error);
    }
    return result.promise;
  }

  close(): void {
    this.closeSocket(NORMAL_CLOSE);
  }

  private receive(data: RawData, isBinary: boolean): void {
    const bytes = rawDataBytes(data);
    if (isBinary) {
      this.receiveBinary(bytes);
      return;
    }
    this.receiveControl(JSON.parse(new TextDecoder().decode(bytes)) as Record<string, unknown>);
  }

  private receiveBinary(frame: Uint8Array): void {
    if (!this.connectedResult.settled()) {
      throw new Error("Managed process attachment did not begin with connected");
    }
    if (frame.byteLength < 9) {
      throw new Error("Managed process binary frame is shorter than 9 bytes");
    }
    const offset = Number(new DataView(
      frame.buffer,
      frame.byteOffset + 1,
      8,
    ).getBigUint64(0, false));
    frameNumber(offset, "output offset");
    const data = frame.slice(9);
    if (frame[0] === STDOUT_FRAME) {
      if (offset !== this.currentStdoutOffset) {
        throw new Error(
          `Managed process stdout frame offset ${offset} does not match expected ${this.currentStdoutOffset}`,
        );
      }
      this.currentStdoutOffset = offset + data.byteLength;
      this.stdout.push(data);
      return;
    }
    if (frame[0] === STDERR_FRAME) {
      if (offset !== this.currentStderrOffset) {
        throw new Error(
          `Managed process stderr frame offset ${offset} does not match expected ${this.currentStderrOffset}`,
        );
      }
      this.currentStderrOffset = offset + data.byteLength;
      this.stderr.push(data);
      return;
    }
    throw new Error(`Unknown managed process binary frame type 0x${frame[0]!.toString(16)}`);
  }

  private receiveControl(frame: Record<string, unknown>): void {
    const type = frameString(frame.type, "type");
    if (!this.connectedResult.settled() && type !== "connected") {
      throw new Error("Managed process attachment did not begin with connected");
    }
    switch (type) {
      case "connected": {
        const connected = {
          processId: frameString(frame.processId, "processId"),
          stdinSequence: frameNumber(frame.stdinSequence, "stdinSequence"),
          stdoutOffset: frameNumber(frame.stdoutOffset, "stdoutOffset"),
          stderrOffset: frameNumber(frame.stderrOffset, "stderrOffset"),
        };
        this.currentStdinSequence = connected.stdinSequence;
        this.connectedResult.resolve(connected);
        return;
      }
      case "stdin_ack": {
        const sequence = frameNumber(frame.sequence, "sequence");
        this.currentStdinSequence = Math.max(this.currentStdinSequence, sequence);
        for (const [pendingSequence, result] of this.pendingWrites) {
          if (pendingSequence > sequence) continue;
          result.resolve();
          this.pendingWrites.delete(pendingSequence);
        }
        return;
      }
      case "stdin_eof": {
        const sequence = frameNumber(frame.sequence, "sequence");
        this.currentStdinSequence = sequence;
        if (this.stdinEOF?.sequence === sequence) this.stdinEOF.result.resolve();
        return;
      }
      case "stdout_eof": {
        if (typeof frame.clean !== "boolean") {
          throw new Error("Managed process control frame has invalid clean");
        }
        const eof = {
          offset: frameNumber(frame.offset, "offset"),
          clean: frame.clean,
        };
        if (eof.offset !== this.currentStdoutOffset) {
          throw new Error(
            `Managed process stdout EOF offset ${eof.offset} does not match expected ${this.currentStdoutOffset}`,
          );
        }
        this.stdoutEOFResult.resolve(eof);
        this.stdout.end();
        return;
      }
      case "stderr_eof": {
        if (typeof frame.clean !== "boolean") {
          throw new Error("Managed process control frame has invalid clean");
        }
        const eof = {
          offset: frameNumber(frame.offset, "offset"),
          clean: frame.clean,
        };
        if (eof.offset !== this.currentStderrOffset) {
          throw new Error(
            `Managed process stderr EOF offset ${eof.offset} does not match expected ${this.currentStderrOffset}`,
          );
        }
        this.stderrEOFResult.resolve(eof);
        this.stderr.end();
        return;
      }
      case "gap": {
        const gap = {
          stream: frame.stream === "stdout" ? "stdout" as const : frame.stream === "stderr" ? "stderr" as const : (() => {
            throw new Error("Managed process gap has invalid stream");
          })(),
          requestedOffset: frameNumber(frame.requestedOffset, "requestedOffset"),
          retainedFrom: frameNumber(frame.retainedFrom, "retainedFrom"),
        };
        if (gap.stream === "stdout") this.currentStdoutOffset = gap.retainedFrom;
        else this.currentStderrOffset = gap.retainedFrom;
        this.gaps.push(gap);
        return;
      }
      case "exit":
        this.exitResult.resolve({
          exitCode: frame.exitCode === null ? null : frameNumber(frame.exitCode, "exitCode"),
          signal: frame.signal === null ? null : frameString(frame.signal, "signal"),
        });
        return;
      case "error": {
        const code = frameString(frame.code, "code");
        const message = frameString(frame.message, "message");
        this.fail(new Error(`${code}: ${message}`));
        return;
      }
      default:
        throw new Error(`Unknown managed process control frame type ${type}`);
    }
  }

  private protocolFailure(reason: unknown): void {
    this.fail(reason);
    this.closeSocket(POLICY_VIOLATION_CLOSE, "invalid managed process frame");
  }

  private updateReceiveFlow(): void {
    const bufferedBytes = this.stdout.bufferedWeight + this.stderr.bufferedWeight;
    if (!this.receivePaused && bufferedBytes >= WEB_SOCKET_OUTPUT_HIGH_WATER_BYTES) {
      this.receivePaused = true;
      this.socket.pause();
    } else if (this.receivePaused && bufferedBytes <= WEB_SOCKET_OUTPUT_LOW_WATER_BYTES) {
      this.receivePaused = false;
      this.socket.resume();
    }
  }

  private closeSocket(code: number, reason?: string): void {
    if (this.receivePaused) {
      this.receivePaused = false;
      this.socket.resume();
    }
    this.socket.close(code, reason);
  }

  private fail(reason: unknown, normal = false): void {
    if (this.failed) return;
    this.failed = true;
    const error = reason instanceof Error ? reason : new Error(String(reason));
    this.connectedResult.reject(error);
    this.stdoutEOFResult.reject(error);
    this.stderrEOFResult.reject(error);
    this.exitResult.reject(error);
    this.stdinEOF?.result.reject(error);
    for (const result of this.pendingWrites.values()) result.reject(error);
    this.pendingWrites.clear();
    if (normal) {
      this.stdout.end();
      this.stderr.end();
      this.gaps.end();
    } else {
      this.stdout.fail(error);
      this.stderr.fail(error);
      this.gaps.fail(error);
    }
  }
}

/** Opens the Node.js WebSocket transport used by the managed-process facade. */
export async function openManagedProcessAttachment(options: {
  baseUrl: string;
  headers?: Record<string, string>;
  processId: string;
  request: ManagedProcessAttachRequest;
  signal?: AbortSignal;
}): Promise<ManagedProcessAttachment> {
  const { default: WebSocketClient } = await import("ws");
  const socket = new WebSocketClient(
    managedProcessWebSocketUrl(options.baseUrl, options.processId, options.request),
    { headers: options.headers },
  );
  return new ManagedProcessAttachmentImpl(socket, options.request, options.signal);
}
