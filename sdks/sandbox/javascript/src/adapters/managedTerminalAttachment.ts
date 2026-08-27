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
  ManagedTerminalAttachRequest,
  ManagedTerminalConnected,
  ManagedTerminalExit,
  ManagedTerminalOutputEOF,
  ManagedTerminalOutputGap,
} from "../models/managedTerminal.js";
import type { ManagedTerminalAttachment } from "../services/managedTerminals.js";
import {
  AsyncQueue,
  deferred,
  rawDataBytes,
  WEB_SOCKET_OUTPUT_HIGH_WATER_BYTES,
  WEB_SOCKET_OUTPUT_LOW_WATER_BYTES,
} from "./webSocketQueue.js";

const INPUT_FRAME = 0x00;
const OUTPUT_FRAME = 0x01;
const NORMAL_CLOSE = 1000;
const POLICY_VIOLATION_CLOSE = 1008;

function managedTerminalWebSocketUrl(
  baseUrl: string,
  terminalId: string,
  request: ManagedTerminalAttachRequest,
): string {
  const base = baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`;
  const endpoint = new URL(
    `v1/terminals/${encodeURIComponent(terminalId)}/io`,
    base,
  );
  if (endpoint.protocol === "http:") endpoint.protocol = "ws:";
  else if (endpoint.protocol === "https:") endpoint.protocol = "wss:";
  else throw new Error(`Unsupported execd URL scheme: ${endpoint.protocol}`);
  endpoint.searchParams.set("outputOffset", String(request.outputOffset));
  return endpoint.toString();
}

function frameNumber(value: unknown, field: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    throw new Error(`Managed terminal control frame has invalid ${field}`);
  }
  return value;
}

function frameString(value: unknown, field: string): string {
  if (typeof value !== "string") {
    throw new Error(`Managed terminal control frame has invalid ${field}`);
  }
  return value;
}

class ManagedTerminalAttachmentImpl implements ManagedTerminalAttachment {
  readonly output: AsyncQueue<Uint8Array>;
  readonly gaps = new AsyncQueue<ManagedTerminalOutputGap>();

  private readonly connectedResult = deferred<ManagedTerminalConnected>();
  private readonly outputEOFResult = deferred<ManagedTerminalOutputEOF>();
  private readonly exitResult = deferred<ManagedTerminalExit>();
  private failed = false;
  private receivePaused = false;
  private abortListener: (() => void) | undefined;
  private currentOutputOffset: number;

  readonly connected = this.connectedResult.promise;
  readonly outputEOF = this.outputEOFResult.promise;
  readonly exit = this.exitResult.promise;

  constructor(
    private readonly socket: WebSocket,
    request: ManagedTerminalAttachRequest,
    private readonly signal?: AbortSignal,
  ) {
    this.output = new AsyncQueue(
      (data) => data.byteLength,
      () => this.updateReceiveFlow(),
    );
    this.currentOutputOffset = request.outputOffset;
    for (const promise of [this.connected, this.outputEOF, this.exit]) {
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
      this.fail(
        new Error(`Managed terminal attachment closed (${code})${detail}`),
        code === NORMAL_CLOSE,
      );
      if (this.signal && this.abortListener) {
        this.signal.removeEventListener("abort", this.abortListener);
      }
    });

    if (signal) {
      this.abortListener = () => {
        const reason = signal.reason instanceof Error
          ? signal.reason
          : new Error("Managed terminal attachment aborted");
        this.fail(reason);
        this.closeSocket(NORMAL_CLOSE, "aborted");
      };
      if (signal.aborted) this.abortListener();
      else signal.addEventListener("abort", this.abortListener, { once: true });
    }
  }

  get outputOffset(): number {
    return this.currentOutputOffset;
  }

  async write(data: Uint8Array): Promise<void> {
    const frame = new Uint8Array(1 + data.byteLength);
    frame[0] = INPUT_FRAME;
    frame.set(data, 1);
    await this.send(frame);
  }

  async resize(rows: number, cols: number): Promise<void> {
    if (!Number.isSafeInteger(rows) || rows < 1 || rows > 65_535) {
      throw new Error("Managed terminal rows must be an integer from 1 through 65535");
    }
    if (!Number.isSafeInteger(cols) || cols < 1 || cols > 65_535) {
      throw new Error("Managed terminal cols must be an integer from 1 through 65535");
    }
    await this.send(JSON.stringify({ type: "resize", rows, cols }));
  }

  close(): void {
    this.closeSocket(NORMAL_CLOSE);
  }

  private async send(data: Uint8Array | string): Promise<void> {
    await this.connected;
    const result = deferred<void>();
    void result.promise.catch(() => undefined);
    try {
      this.socket.send(data, (error) => {
        if (error) result.reject(error);
        else result.resolve();
      });
    } catch (error) {
      result.reject(error);
    }
    return result.promise;
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
      throw new Error("Managed terminal attachment did not begin with connected");
    }
    if (frame.byteLength < 9) {
      throw new Error("Managed terminal binary frame is shorter than 9 bytes");
    }
    if (frame[0] !== OUTPUT_FRAME) {
      throw new Error(`Unknown managed terminal binary frame type 0x${frame[0]!.toString(16)}`);
    }
    const offset = Number(new DataView(
      frame.buffer,
      frame.byteOffset + 1,
      8,
    ).getBigUint64(0, false));
    frameNumber(offset, "output offset");
    if (offset !== this.currentOutputOffset) {
      throw new Error(
        `Managed terminal output frame offset ${offset} does not match expected ${this.currentOutputOffset}`,
      );
    }
    const output = frame.slice(9);
    this.currentOutputOffset = offset + output.byteLength;
    this.output.push(output);
  }

  private receiveControl(frame: Record<string, unknown>): void {
    const type = frameString(frame.type, "type");
    if (!this.connectedResult.settled() && type !== "connected") {
      throw new Error("Managed terminal attachment did not begin with connected");
    }
    switch (type) {
      case "connected":
        this.connectedResult.resolve({
          terminalId: frameString(frame.terminalId, "terminalId"),
          outputOffset: frameNumber(frame.outputOffset, "outputOffset"),
        });
        return;
      case "gap": {
        const gap = {
          requestedOffset: frameNumber(frame.requestedOffset, "requestedOffset"),
          retainedFrom: frameNumber(frame.retainedFrom, "retainedFrom"),
        };
        this.currentOutputOffset = gap.retainedFrom;
        this.gaps.push(gap);
        return;
      }
      case "output_eof": {
        const offset = frameNumber(frame.offset, "offset");
        if (offset !== this.currentOutputOffset) {
          throw new Error(
            `Managed terminal output EOF offset ${offset} does not match expected ${this.currentOutputOffset}`,
          );
        }
        this.outputEOFResult.resolve({ offset });
        this.output.end();
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
        throw new Error(`Unknown managed terminal control frame type ${type}`);
    }
  }

  private protocolFailure(reason: unknown): void {
    this.fail(reason);
    this.closeSocket(POLICY_VIOLATION_CLOSE, "invalid managed terminal frame");
  }

  private updateReceiveFlow(): void {
    const bufferedBytes = this.output.bufferedWeight;
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
    this.outputEOFResult.reject(error);
    this.exitResult.reject(error);
    if (normal) {
      this.output.end();
      this.gaps.end();
    } else {
      this.output.fail(error);
      this.gaps.fail(error);
    }
  }
}

/** Opens the Node.js WebSocket transport used by the managed-terminal facade. */
export async function openManagedTerminalAttachment(options: {
  baseUrl: string;
  headers?: Record<string, string>;
  terminalId: string;
  request: ManagedTerminalAttachRequest;
  signal?: AbortSignal;
}): Promise<ManagedTerminalAttachment> {
  const { default: WebSocketClient } = await import("ws");
  const socket = new WebSocketClient(
    managedTerminalWebSocketUrl(options.baseUrl, options.terminalId, options.request),
    { headers: options.headers },
  );
  return new ManagedTerminalAttachmentImpl(socket, options.request, options.signal);
}
