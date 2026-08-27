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

import {
  isExecdTransportError,
  readExecdResponseText,
} from "../openapi/execdClient.js";
import { throwOnOpenApiFetchError } from "./openapiError.js";

interface ManagedCreateResult {
  error?: unknown;
  response: Response;
}

async function readPublication<T>(
  attempt: () => Promise<ManagedCreateResult>,
  message: string,
): Promise<T> {
  const { error, response } = await attempt();
  throwOnOpenApiFetchError({ error, response }, message);
  const body = await readExecdResponseText(response);
  if (body === "") throw new Error(`${message}: response body is empty`);
  return JSON.parse(body) as T;
}

/** Replays one idempotent managed create after an outcome-unknown transport failure. */
export async function recoverManagedCreatePublication<T>(
  attempt: () => Promise<ManagedCreateResult>,
  signal: AbortSignal | undefined,
  message: string,
): Promise<T> {
  try {
    return await readPublication<T>(attempt, message);
  } catch (error) {
    if (signal?.aborted || !isExecdTransportError(error)) throw error;
  }
  return readPublication<T>(attempt, message);
}
