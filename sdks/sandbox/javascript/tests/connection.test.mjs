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

import assert from "node:assert/strict";
import test from "node:test";

import { ConnectionConfig, DefaultAdapterFactory } from "../dist/index.js";

test("ConnectionConfig strips trailing slash suffix without regex backtracking", () => {
  const connectionConfig = new ConnectionConfig({
    domain: `https://api.opensandbox.test${"/".repeat(4096)}`,
  });

  assert.equal(connectionConfig.getBaseUrl(), "https://api.opensandbox.test/v1");
});

test("ConnectionConfig preserves path prefix while normalizing v1 suffix", () => {
  const connectionConfig = new ConnectionConfig({
    domain: "https://api.opensandbox.test/proxy/v1/",
  });

  assert.equal(connectionConfig.getBaseUrl(), "https://api.opensandbox.test/proxy/v1");
});

// Regression: the default User-Agent version is hand-maintained and must be
// bumped together with the package version. Update this expectation when releasing.
test("ConnectionConfig default userAgent matches package version", () => {
  const connectionConfig = new ConnectionConfig({
    domain: "https://api.opensandbox.test",
  });

  assert.equal(connectionConfig.userAgent, "OpenSandbox-JS-SDK/0.1.12");
});

test("ConnectionConfig.disableMetrics is preserved by withTransportIfMissing", () => {
  const connectionConfig = new ConnectionConfig({
    domain: "https://api.opensandbox.test",
    disableMetrics: true,
  });
  assert.equal(connectionConfig.disableMetrics, true);
  assert.equal(connectionConfig.withTransportIfMissing().disableMetrics, true);
});

test("DefaultAdapterFactory propagates caller aborts to managed create requests", async (t) => {
  const originalFetch = globalThis.fetch;
  let requestStarted;

  globalThis.fetch = async (input, init) => {
    requestStarted?.();
    const signal = init?.signal ?? (input instanceof Request ? input.signal : undefined);
    return await new Promise((_, reject) => {
      const onAbort = () => reject(signal.reason);
      if (signal.aborted) onAbort();
      else signal.addEventListener("abort", onAbort, { once: true });
    });
  };

  const connectionConfig = new ConnectionConfig({
    domain: "http://sandbox.test",
    requestTimeoutSeconds: 10,
  }).withTransportIfMissing();
  const stack = new DefaultAdapterFactory().createExecdStack({
    connectionConfig,
    execdBaseUrl: "http://sandbox.test",
  });

  try {
    const cases = [
      {
        name: "process",
        create: (signal) => stack.processes.create({
          operationId: "process-abort",
          argv: ["/bin/true"],
          cwd: "/workspace",
          stdin: "pipe",
        }, signal),
      },
      {
        name: "terminal",
        create: (signal) => stack.terminals.create({
          operationId: "terminal-abort",
          argv: ["/bin/sh"],
          cwd: "/workspace",
          rows: 24,
          cols: 80,
        }, signal),
      },
    ];

    for (const testCase of cases) {
      await t.test(testCase.name, async () => {
        const started = new Promise((resolve) => {
          requestStarted = resolve;
        });
        const controller = new AbortController();
        const reason = new Error(`cancel ${testCase.name} create`);
        const handle = testCase.create(controller.signal);

        await started;
        controller.abort(reason);

        await assert.rejects(handle.ready, (error) => error === reason);
      });
    }
  } finally {
    await connectionConfig.closeTransport();
    globalThis.fetch = originalFetch;
  }
});
