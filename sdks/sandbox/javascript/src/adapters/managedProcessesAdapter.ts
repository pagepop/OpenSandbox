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
  ManagedProcessReady,
  ManagedProcessStatus,
  ResolveExecutableRequest,
  ResolveExecutableResponse,
  TerminateManagedProcessRequest,
} from "../models/managedProcess.js";
import type {
  ManagedProcessAttachment,
  ManagedProcessHandle,
  ManagedProcesses,
} from "../services/managedProcesses.js";
import type { ExecdClient } from "../openapi/execdClient.js";
import { waitForPublication } from "./abortablePublication.js";
import { openManagedProcessAttachment } from "./managedProcessAttachment.js";
import { throwOnOpenApiFetchError } from "./openapiError.js";

function requireData<T>(data: T | undefined, message: string): T {
  if (data === undefined) throw new Error(`${message}: response body is empty`);
  return data;
}

/** Connection inputs for managed-process WebSocket attachments. */
export interface ManagedProcessesAdapterOptions {
  baseUrl: string;
  headers?: Record<string, string>;
}

class ManagedProcessHandleImpl implements ManagedProcessHandle {
  readonly ready: Promise<ManagedProcessReady>;
  private publishedProcessId: string | undefined;
  private publishedPid: number | undefined;

  constructor(
    publication: Promise<ManagedProcessStatus>,
    private readonly processes: ManagedProcessesAdapter,
  ) {
    this.ready = publication.then((status) => {
      const pid = status.pid;
      if (!status.processId || typeof pid !== "number" || !Number.isInteger(pid)) {
        throw new Error("Create managed process failed: publication response omitted processId or pid");
      }
      this.publishedProcessId = status.processId;
      this.publishedPid = pid;
      return { pid };
    });
    void this.ready.catch(() => undefined);
  }

  get processId(): string | undefined {
    return this.publishedProcessId;
  }

  get pid(): number | undefined {
    return this.publishedPid;
  }

  async get(signal?: AbortSignal): Promise<ManagedProcessStatus> {
    return this.processes.get(await this.requireProcessId(signal), signal);
  }

  async terminate(
    request?: TerminateManagedProcessRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessStatus> {
    return this.processes.terminate(
      await this.requireProcessId(signal),
      request,
      signal,
    );
  }

  async delete(signal?: AbortSignal): Promise<void> {
    await this.processes.delete(await this.requireProcessId(signal), signal);
  }

  async attach(
    request: ManagedProcessAttachRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessAttachment> {
    return this.processes.attach(
      await this.requireProcessId(signal),
      request,
      signal,
    );
  }

  private async requireProcessId(signal?: AbortSignal): Promise<string> {
    await waitForPublication(this.ready, signal);
    return this.publishedProcessId!;
  }
}

/** Facade over OSEP-0015 managed-process control and I/O routes. */
export class ManagedProcessesAdapter implements ManagedProcesses {
  constructor(
    private readonly client: ExecdClient,
    private readonly opts: ManagedProcessesAdapterOptions,
  ) {}

  async resolveExecutable(
    request: ResolveExecutableRequest,
    signal?: AbortSignal,
  ): Promise<ResolveExecutableResponse> {
    const { data, error, response } = await this.client.POST(
      "/v1/processes/resolve-executable",
      { body: request, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Resolve managed executable failed");
    return requireData(data, "Resolve managed executable failed");
  }

  create(
    request: CreateManagedProcessRequest,
    signal?: AbortSignal,
  ): ManagedProcessHandle {
    const publication = this.createStatus(request, signal);
    return new ManagedProcessHandleImpl(publication, this);
  }

  async get(processId: string, signal?: AbortSignal): Promise<ManagedProcessStatus> {
    const { data, error, response } = await this.client.GET(
      "/v1/processes/{processId}",
      { params: { path: { processId } }, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Get managed process failed");
    return requireData(data, "Get managed process failed");
  }

  async terminate(
    processId: string,
    request?: TerminateManagedProcessRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessStatus> {
    const { data, error, response } = await this.client.POST(
      "/v1/processes/{processId}/terminate",
      { params: { path: { processId } }, body: request, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Terminate managed process failed");
    return requireData(data, "Terminate managed process failed");
  }

  async delete(processId: string, signal?: AbortSignal): Promise<void> {
    const { error, response } = await this.client.DELETE(
      "/v1/processes/{processId}",
      { params: { path: { processId } }, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Delete managed process failed");
  }

  attach(
    processId: string,
    request: ManagedProcessAttachRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessAttachment> {
    return openManagedProcessAttachment({
      baseUrl: this.opts.baseUrl,
      headers: this.opts.headers,
      processId,
      request,
      signal,
    });
  }

  private async createStatus(
    request: CreateManagedProcessRequest,
    signal?: AbortSignal,
  ): Promise<ManagedProcessStatus> {
    const { data, error, response } = await this.client.POST(
      "/v1/processes",
      { body: request, signal },
    );
    throwOnOpenApiFetchError({ error, response }, "Create managed process failed");
    return requireData(data, "Create managed process failed");
  }
}
