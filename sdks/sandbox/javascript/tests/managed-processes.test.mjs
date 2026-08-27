import assert from "node:assert/strict";
import test from "node:test";

import { createExecdClient, ManagedProcessesAdapter } from "../dist/internal.js";
import { deferred } from "./helpers.mjs";

function managedProcesses(options) {
  const client = createExecdClient(options);
  return new ManagedProcessesAdapter(client, {
    baseUrl: options.baseUrl,
    headers: options.headers,
  });
}

function processStatus(overrides = {}) {
  return {
    processId: "proc/1",
    pid: 4321,
    state: "running",
    exitCode: null,
    signal: null,
    topLevelExited: false,
    treeEmpty: false,
    stdinSequence: 0,
    stdoutOffset: 0,
    stderrOffset: 0,
    stdoutRetainedFrom: 0,
    stderrRetainedFrom: 0,
    stdoutSpillPath: null,
    stderrSpillPath: null,
    ...overrides,
  };
}

test("ManagedProcessesAdapter maps control routes and deferred publication", async () => {
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

    if (request.url.endsWith("/v1/processes/resolve-executable")) {
      return Response.json({ path: "/usr/bin/node" });
    }
    if (request.method === "POST" && request.url.endsWith("/v1/processes")) {
      await publication.promise;
      return Response.json(processStatus(), { status: 201 });
    }
    if (request.method === "GET") {
      return Response.json(processStatus({ stdoutOffset: 12 }));
    }
    if (request.url.endsWith("/terminate")) {
      return Response.json(processStatus({
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

  const processes = managedProcesses({
    baseUrl: "http://sandbox.test/",
    fetch: fetchImpl,
    headers: { "x-execd-access-token": "token" },
  });

  assert.deepEqual(await processes.resolveExecutable({
    executable: "node",
    env: { PATH: "/usr/bin", REMOVED: null },
  }), { path: "/usr/bin/node" });

  const handle = processes.create({
    operationId: "operation-1",
    argv: ["/usr/bin/node", "script with spaces.js", ""],
    cwd: "/workspace",
    env: { LANG: "C.UTF-8", REMOVED: null },
    stdin: "pipe",
    stdoutRetentionBytes: 1024,
    stderrRetentionBytes: 2048,
    graceMs: 3000,
  });
  assert.equal(handle.processId, undefined);
  assert.equal(handle.pid, undefined);
  publication.resolve();

  assert.deepEqual(await handle.ready, { pid: 4321 });
  assert.equal(handle.processId, "proc/1");
  assert.equal(handle.pid, 4321);
  assert.equal((await handle.get()).stdoutOffset, 12);
  assert.equal((await handle.terminate({ graceMs: 0 })).treeEmpty, true);
  assert.equal((await handle.terminate()).treeEmpty, true);
  await handle.delete();

  assert.deepEqual(calls[0].body, {
    executable: "node",
    env: { PATH: "/usr/bin", REMOVED: null },
  });
  assert.deepEqual(calls[1].body, {
    operationId: "operation-1",
    argv: ["/usr/bin/node", "script with spaces.js", ""],
    cwd: "/workspace",
    env: { LANG: "C.UTF-8", REMOVED: null },
    stdin: "pipe",
    stdoutRetentionBytes: 1024,
    stderrRetentionBytes: 2048,
    graceMs: 3000,
  });
  assert.equal(calls[1].headers["x-execd-access-token"], "token");
  assert.equal(calls[2].url, "http://sandbox.test/v1/processes/proc%2F1");
  assert.deepEqual(calls[3].body, { graceMs: 0 });
  assert.equal(calls[4].body, undefined);
  assert.equal(calls[5].method, "DELETE");
});

test("ManagedProcessesAdapter exposes create failures through ready", async () => {
  const processes = managedProcesses({
    baseUrl: "http://sandbox.test",
    fetch: async () => new Response("spawn failed", { status: 500 }),
  });

  const handle = processes.create({
    operationId: "operation-failed",
    argv: ["/missing"],
    cwd: "/workspace",
    stdin: "pipe",
  });

  await assert.rejects(handle.ready, /Create managed process failed/);
  assert.equal(handle.processId, undefined);
  assert.equal(handle.pid, undefined);
});

test("ManagedProcessesAdapter rejects a create response without publication facts", async () => {
  const processes = managedProcesses({
    baseUrl: "http://sandbox.test",
    fetch: async () => Response.json(processStatus({ pid: undefined }), { status: 201 }),
  });

  const handle = processes.create({
    operationId: "operation-unpublished",
    argv: ["/bin/true"],
    cwd: "/workspace",
    stdin: "pipe",
  });

  await assert.rejects(handle.ready, /publication response omitted processId or pid/);
});

test("ManagedProcessHandle operations can abort while publication is pending", async () => {
  const createStarted = deferred();
  const releaseCreate = deferred();
  let requestCount = 0;
  const processes = managedProcesses({
    baseUrl: "http://sandbox.test",
    fetch: async () => {
      requestCount += 1;
      createStarted.resolve();
      await releaseCreate.promise;
      return Response.json(processStatus(), { status: 201 });
    },
  });
  const handle = processes.create({
    operationId: "operation-cancelled-waits",
    argv: ["/bin/sh"],
    cwd: "/workspace",
    stdin: "pipe",
  });
  await createStarted.promise;

  const calls = [
    (signal) => handle.get(signal),
    (signal) => handle.terminate(undefined, signal),
    (signal) => handle.delete(signal),
    (signal) => handle.attach({
      stdinSequence: 0,
      stdoutOffset: 0,
      stderrOffset: 0,
    }, signal),
  ];
  const reason = new Error("stop waiting for process publication");
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
