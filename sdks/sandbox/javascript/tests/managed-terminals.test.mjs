import assert from "node:assert/strict";
import test from "node:test";

import { createExecdClient, ManagedTerminalsAdapter } from "../dist/internal.js";
import { deferred } from "./helpers.mjs";

function managedTerminals(options) {
  const client = createExecdClient(options);
  return new ManagedTerminalsAdapter(client, {
    baseUrl: options.baseUrl,
    headers: options.headers,
  });
}

function terminalStatus(overrides = {}) {
  return {
    terminalId: "term/1",
    pid: 5432,
    state: "running",
    exitCode: null,
    signal: null,
    topLevelExited: false,
    treeEmpty: false,
    outputOffset: 0,
    outputRetainedFrom: 0,
    outputEof: false,
    ...overrides,
  };
}

test("ManagedTerminalsAdapter maps control routes and deferred publication", async () => {
  const publication = deferred();
  const calls = [];
  const fetchImpl = async (url, init) => {
    const wireRequest = url instanceof Request ? url : new Request(url, init);
    const text = await wireRequest.text();
    const request = {
      url: wireRequest.url,
      method: wireRequest.method,
      headers: Object.fromEntries(wireRequest.headers),
      body: text === "" ? undefined : JSON.parse(text),
    };
    calls.push(request);

    if (request.method === "POST" && request.url.endsWith("/v1/terminals")) {
      await publication.promise;
      return Response.json(terminalStatus(), { status: 201 });
    }
    if (request.method === "GET" && request.url.endsWith("/foreground")) {
      return Response.json({ processGroup: 5432, inputWaiting: true });
    }
    if (request.url.endsWith("/foreground/signal")) {
      return Response.json({ processGroup: 5432 });
    }
    if (request.method === "GET") {
      return Response.json(terminalStatus({ outputOffset: 12 }));
    }
    if (request.url.endsWith("/terminate")) {
      return Response.json(terminalStatus({
        state: "quiescent",
        signal: "SIGKILL",
        topLevelExited: true,
        treeEmpty: true,
      }));
    }
    if (request.method === "DELETE") {
      return new Response(null, { status: 204 });
    }
    return new Response("not found", { status: 404 });
  };

  const terminals = managedTerminals({
    baseUrl: "http://sandbox.test/",
    fetch: fetchImpl,
    headers: { "x-execd-access-token": "token" },
  });
  const handle = terminals.create({
    operationId: "operation-1",
    argv: ["/usr/bin/bash", "-l"],
    cwd: "/workspace",
    env: { LANG: "C.UTF-8", REMOVED: null },
    rows: 40,
    cols: 120,
    graceMs: 3000,
  });
  assert.equal(handle.terminalId, undefined);
  assert.equal(handle.pid, undefined);
  publication.resolve();

  assert.deepEqual(await handle.ready, { pid: 5432 });
  assert.equal(handle.terminalId, "term/1");
  assert.equal(handle.pid, 5432);
  assert.equal((await handle.get()).outputOffset, 12);
  assert.deepEqual(await handle.foreground(), {
    processGroup: 5432,
    inputWaiting: true,
  });
  assert.equal(await handle.signalForeground({ signal: "SIGINT" }), 5432);
  assert.equal((await handle.terminate({ graceMs: 0 })).treeEmpty, true);
  assert.equal((await handle.terminate()).treeEmpty, true);
  await handle.delete();

  assert.deepEqual(calls[0].body, {
    operationId: "operation-1",
    argv: ["/usr/bin/bash", "-l"],
    cwd: "/workspace",
    env: { LANG: "C.UTF-8", REMOVED: null },
    rows: 40,
    cols: 120,
    graceMs: 3000,
  });
  assert.equal(calls[0].headers["x-execd-access-token"], "token");
  assert.equal(calls[1].url, "http://sandbox.test/v1/terminals/term%2F1");
  assert.deepEqual(calls[3].body, { signal: "SIGINT" });
  assert.deepEqual(calls[4].body, { graceMs: 0 });
  assert.equal(calls[5].body, undefined);
  assert.equal(calls[6].method, "DELETE");
});

test("ManagedTerminalsAdapter exposes create failures through ready", async () => {
  let requestCount = 0;
  const terminals = managedTerminals({
    baseUrl: "http://sandbox.test",
    fetch: async () => {
      requestCount += 1;
      return new Response("PTY failed", { status: 500 });
    },
  });
  const handle = terminals.create({
    operationId: "operation-failed",
    argv: ["/missing"],
    cwd: "/workspace",
    rows: 24,
    cols: 80,
  });

  await assert.rejects(handle.ready, /PTY failed/);
  assert.equal(handle.terminalId, undefined);
  assert.equal(handle.pid, undefined);
  assert.equal(requestCount, 1);
});

test("ManagedTerminalsAdapter recovers an interrupted create response body", async () => {
  const bodies = [];
  const terminals = managedTerminals({
    baseUrl: "http://sandbox.test",
    fetch: async (request) => {
      bodies.push(JSON.parse(await request.text()));
      if (bodies.length === 1) {
        const body = new ReadableStream({
          start(stream) {
            stream.enqueue(new TextEncoder().encode('{"terminalId":"term/1"'));
            stream.error(new TypeError("response stream interrupted"));
          },
        });
        return new Response(body, {
          status: 201,
          headers: { "content-type": "application/json" },
        });
      }
      return Response.json(terminalStatus(), { status: 200 });
    },
  });

  const handle = terminals.create({
    operationId: "operation-recover",
    argv: ["/bin/bash", "-l"],
    cwd: "/workspace",
    rows: 24,
    cols: 80,
  });

  assert.deepEqual(await handle.ready, { pid: 5432 });
  assert.equal(handle.terminalId, "term/1");
  assert.equal(bodies.length, 2);
  assert.deepEqual(bodies[1], bodies[0]);
  assert.equal(bodies[1].operationId, "operation-recover");
});

test("ManagedTerminalsAdapter rejects a create response without publication facts", async () => {
  const terminals = managedTerminals({
    baseUrl: "http://sandbox.test",
    fetch: async () => Response.json(terminalStatus({ pid: undefined }), { status: 201 }),
  });
  const handle = terminals.create({
    operationId: "operation-unpublished",
    argv: ["/bin/sh"],
    cwd: "/workspace",
    rows: 24,
    cols: 80,
  });

  await assert.rejects(handle.ready, /publication response omitted terminalId or pid/);
});

test("ManagedTerminalHandle operations can abort while publication is pending", async () => {
  const createStarted = deferred();
  const releaseCreate = deferred();
  let requestCount = 0;
  const terminals = managedTerminals({
    baseUrl: "http://sandbox.test",
    fetch: async () => {
      requestCount += 1;
      createStarted.resolve();
      await releaseCreate.promise;
      return Response.json(terminalStatus(), { status: 201 });
    },
  });
  const handle = terminals.create({
    operationId: "operation-cancelled-waits",
    argv: ["/bin/sh"],
    cwd: "/workspace",
    rows: 24,
    cols: 80,
  });
  await createStarted.promise;

  const calls = [
    (signal) => handle.get(signal),
    (signal) => handle.foreground(signal),
    (signal) => handle.signalForeground({ signal: "SIGINT" }, signal),
    (signal) => handle.terminate(undefined, signal),
    (signal) => handle.delete(signal),
    (signal) => handle.attach({ outputOffset: 0 }, signal),
  ];
  const reason = new Error("stop waiting for terminal publication");
  const controllers = calls.map(() => new AbortController());
  const pending = calls.map((call, index) => call(controllers[index].signal));
  for (const controller of controllers) controller.abort(reason);
  const outcomes = await Promise.all(pending.map((promise) =>
    promise.then(() => undefined, (error) => error)));
  for (const outcome of outcomes) assert.equal(outcome, reason);
  assert.equal(requestCount, 1);

  releaseCreate.resolve();
  await handle.ready;
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(requestCount, 1);
});
