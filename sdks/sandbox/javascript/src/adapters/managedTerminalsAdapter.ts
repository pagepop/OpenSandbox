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
  ManagedTerminalForeground,
  ManagedTerminalReady,
  ManagedTerminalStatus,
  SignalManagedTerminalForegroundRequest,
  TerminateManagedTerminalRequest,
} from "../models/managedTerminal.js";
import type {
  ManagedTerminalAttachment,
  ManagedTerminalHandle,
  ManagedTerminals,
} from "../services/managedTerminals.js";
import type { ExecdClient } from "../openapi/execdClient.js";
import { waitForPublication } from "./abortablePublication.js";
import { recoverManagedCreatePublication } from "./managedCreatePublication.js";
import { openManagedTerminalAttachment } from "./managedTerminalAttachment.js";
import { throwOnOpenApiFetchError } from "./openapiError.js";

function requireData<T>(data: T | undefined, message: string): T {
  if (data === undefined) throw new Error(`${message}: response body is empty`);
  return data;
}

/** Connection inputs for managed-terminal control and I/O. */
export interface ManagedTerminalsAdapterOptions {
  baseUrl: string;
  headers?: Record<string, string>;
}

class ManagedTerminalHandleImpl implements ManagedTerminalHandle {
  readonly ready: Promise<ManagedTerminalReady>;
  private publishedTerminalId: string | undefined;
  private publishedPid: number | undefined;

  constructor(
    publication: Promise<ManagedTerminalStatus>,
    private readonly terminals: ManagedTerminalsAdapter,
  ) {
    this.ready = publication.then((status) => {
      const pid = status.pid;
      if (!status.terminalId || typeof pid !== "number" || !Number.isInteger(pid)) {
        throw new Error("Create managed terminal failed: publication response omitted terminalId or pid");
      }
      this.publishedTerminalId = status.terminalId;
      this.publishedPid = pid;
      return { pid };
    });
    void this.ready.catch(() => undefined);
  }

  get terminalId(): string | undefined {
    return this.publishedTerminalId;
  }

  get pid(): number | undefined {
    return this.publishedPid;
  }

  async get(signal?: AbortSignal): Promise<ManagedTerminalStatus> {
    return this.terminals.get(await this.requireTerminalId(signal), signal);
  }

  async foreground(signal?: AbortSignal): Promise<ManagedTerminalForeground> {
    return this.terminals.foreground(
      await this.requireTerminalId(signal),
      signal,
    );
  }

  async signalForeground(
    request: SignalManagedTerminalForegroundRequest,
    signal?: AbortSignal,
  ): Promise<number> {
    return this.terminals.signalForeground(
      await this.requireTerminalId(signal),
      request,
      signal,
    );
  }

  async terminate(
    request?: TerminateManagedTerminalRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalStatus> {
    return this.terminals.terminate(
      await this.requireTerminalId(signal),
      request,
      signal,
    );
  }

  async delete(signal?: AbortSignal): Promise<void> {
    await this.terminals.delete(await this.requireTerminalId(signal), signal);
  }

  async attach(
    request: ManagedTerminalAttachRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalAttachment> {
    return this.terminals.attach(
      await this.requireTerminalId(signal),
      request,
      signal,
    );
  }

  private async requireTerminalId(signal?: AbortSignal): Promise<string> {
    await waitForPublication(this.ready, signal);
    return this.publishedTerminalId!;
  }
}

/** Facade over OSEP-0023 managed-terminal control and I/O routes. */
export class ManagedTerminalsAdapter implements ManagedTerminals {
  constructor(
    private readonly client: ExecdClient,
    private readonly opts: ManagedTerminalsAdapterOptions,
  ) {}

  create(
    request: CreateManagedTerminalRequest,
    signal?: AbortSignal,
  ): ManagedTerminalHandle {
    const publication = this.createStatus(request, signal);
    return new ManagedTerminalHandleImpl(publication, this);
  }

  async get(terminalId: string, signal?: AbortSignal): Promise<ManagedTerminalStatus> {
    const { data, error, response } = await this.client.GET(
      "/v1/terminals/{terminalId}",
      { params: { path: { terminalId } }, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Get managed terminal failed");
    return requireData(data, "Get managed terminal failed");
  }

  async foreground(
    terminalId: string,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalForeground> {
    const { data, error, response } = await this.client.GET(
      "/v1/terminals/{terminalId}/foreground",
      { params: { path: { terminalId } }, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Get managed terminal foreground failed");
    return requireData(data, "Get managed terminal foreground failed");
  }

  async signalForeground(
    terminalId: string,
    request: SignalManagedTerminalForegroundRequest,
    signal?: AbortSignal,
  ): Promise<number> {
    const { data, error, response } = await this.client.POST(
      "/v1/terminals/{terminalId}/foreground/signal",
      { params: { path: { terminalId } }, body: request, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Signal managed terminal foreground failed");
    return requireData(data, "Signal managed terminal foreground failed").processGroup;
  }

  async terminate(
    terminalId: string,
    request?: TerminateManagedTerminalRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalStatus> {
    const { data, error, response } = await this.client.POST(
      "/v1/terminals/{terminalId}/terminate",
      { params: { path: { terminalId } }, body: request, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Terminate managed terminal failed");
    return requireData(data, "Terminate managed terminal failed");
  }

  async delete(terminalId: string, signal?: AbortSignal): Promise<void> {
    const { error, response } = await this.client.DELETE(
      "/v1/terminals/{terminalId}",
      { params: { path: { terminalId } }, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Delete managed terminal failed");
  }

  attach(
    terminalId: string,
    request: ManagedTerminalAttachRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalAttachment> {
    return openManagedTerminalAttachment({
      baseUrl: this.opts.baseUrl,
      headers: this.opts.headers,
      terminalId,
      request,
      signal,
    });
  }

  private async createStatus(
    request: CreateManagedTerminalRequest,
    signal?: AbortSignal,
  ): Promise<ManagedTerminalStatus> {
    const body = JSON.stringify(request);
    return recoverManagedCreatePublication<ManagedTerminalStatus>(
      () => this.client.POST(
        "/v1/terminals",
        { body: request, bodySerializer: () => body, signal, parseAs: "stream" },
      ),
      signal,
      "Create managed terminal failed",
    );
  }
}
