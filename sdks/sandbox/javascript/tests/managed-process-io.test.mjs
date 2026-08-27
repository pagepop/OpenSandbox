import assert from "node:assert/strict";
import { createServer } from "node:http";
import test from "node:test";

import { WebSocketServer } from "ws";

import { createExecdClient, ManagedProcessesAdapter } from "../dist/internal.js";
import { deferred, trackWebSocketReceiveFlow } from "./helpers.mjs";

function managedProcesses(options) {
  const client = createExecdClient(options);
  return new ManagedProcessesAdapter(client, {
    baseUrl: options.baseUrl,
    headers: options.headers,
  });
}

function outputFrame(type, offset, text) {
  const data = new TextEncoder().encode(text);
  const frame = new Uint8Array(9 + data.byteLength);
  frame[0] = type;
  new DataView(frame.buffer).setBigUint64(1, BigInt(offset), false);
  frame.set(data, 9);
  return frame;
}

test("managed process attachment preserves frames, offsets, gaps, and acknowledgements", async (t) => {
  const server = createServer();
  const webSockets = new WebSocketServer({ server });
  t.after(() => {
    webSockets.close();
    server.close();
  });

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.equal(typeof address, "object");

  let resolveServer;
  let rejectServer;
  const serverDone = new Promise((resolve, reject) => {
    resolveServer = resolve;
    rejectServer = reject;
  });
  let releaseGap;
  const gapGate = new Promise((resolve) => {
    releaseGap = resolve;
  });
  let releaseOutput;
  const outputGate = new Promise((resolve) => {
    releaseOutput = resolve;
  });
  webSockets.once("connection", async (socket, request) => {
    try {
      assert.equal(request.headers["x-execd-access-token"], "token");
      const endpoint = new URL(request.url, "http://sandbox.test");
      assert.equal(endpoint.pathname, "/v1/processes/proc%2F1/io");
      assert.deepEqual(Object.fromEntries(endpoint.searchParams), {
        stdinSequence: "4",
        stdoutOffset: "1",
        stderrOffset: "2",
      });

      socket.send(JSON.stringify({
        type: "connected",
        processId: "proc/1",
        stdinSequence: 4,
        stdoutOffset: 10,
        stderrOffset: 20,
      }));
      await gapGate;
      socket.send(JSON.stringify({
        type: "gap",
        stream: "stdout",
        requestedOffset: 1,
        retainedFrom: 7,
      }));
      await outputGate;
      socket.send(outputFrame(0x01, 7, "out"));
      socket.send(outputFrame(0x02, 2, "err"));

      let messageNumber = 0;
      socket.on("message", (raw, isBinary) => {
        try {
          messageNumber += 1;
          if (messageNumber === 1) {
            assert.equal(isBinary, true);
            const frame = new Uint8Array(raw.buffer, raw.byteOffset, raw.byteLength);
            assert.equal(frame[0], 0x00);
            assert.equal(new DataView(
              frame.buffer,
              frame.byteOffset + 1,
              8,
            ).getBigUint64(0, false), 5n);
            assert.equal(new TextDecoder().decode(frame.slice(9)), "input");
            socket.send(JSON.stringify({ type: "stdin_ack", sequence: 5 }));
            return;
          }

          assert.equal(isBinary, false);
          assert.deepEqual(JSON.parse(raw.toString()), {
            type: "stdin_eof",
            sequence: 6,
          });
          socket.send(JSON.stringify({ type: "stdin_eof", sequence: 6 }));
          socket.send(JSON.stringify({ type: "stdout_eof", offset: 10, clean: true }));
          socket.send(JSON.stringify({ type: "stderr_eof", offset: 5, clean: false }));
          socket.send(JSON.stringify({ type: "exit", exitCode: null, signal: "SIGKILL" }));
          socket.close(1000);
          resolveServer();
        } catch (error) {
          rejectServer(error);
        }
      });
    } catch (error) {
      rejectServer(error);
    }
  });

  const processes = managedProcesses({
    baseUrl: `http://127.0.0.1:${address.port}`,
    headers: { "x-execd-access-token": "token" },
  });
  const attachment = await processes.attach("proc/1", {
    stdinSequence: 4,
    stdoutOffset: 1,
    stderrOffset: 2,
  });

  assert.deepEqual(await attachment.connected, {
    processId: "proc/1",
    stdinSequence: 4,
    stdoutOffset: 10,
    stderrOffset: 20,
  });
  assert.equal(attachment.stdoutOffset, 1);
  assert.equal(attachment.stderrOffset, 2);
  releaseGap();
  const gap = await attachment.gaps[Symbol.asyncIterator]().next();
  assert.deepEqual(gap, {
    value: { stream: "stdout", requestedOffset: 1, retainedFrom: 7 },
    done: false,
  });
  assert.equal(attachment.stdoutOffset, 7);
  assert.equal(attachment.stderrOffset, 2);
  releaseOutput();
  assert.equal(new TextDecoder().decode(
    (await attachment.stdout[Symbol.asyncIterator]().next()).value,
  ), "out");
  assert.equal(new TextDecoder().decode(
    (await attachment.stderr[Symbol.asyncIterator]().next()).value,
  ), "err");
  assert.equal(attachment.stdoutOffset, 10);
  assert.equal(attachment.stderrOffset, 5);

  await attachment.write(5, new TextEncoder().encode("input"));
  assert.equal(attachment.stdinSequence, 5);
  await attachment.closeStdin(6);
  assert.equal(attachment.stdinSequence, 6);
  assert.deepEqual(await attachment.stdoutEOF, { offset: 10, clean: true });
  assert.deepEqual(await attachment.stderrEOF, { offset: 5, clean: false });
  assert.deepEqual(await attachment.exit, { exitCode: null, signal: "SIGKILL" });
  await serverDone;
});

test("managed process attachment applies aggregate output backpressure", { timeout: 5_000 }, async (t) => {
  const server = createServer();
  const webSockets = new WebSocketServer({ server });
  t.after(() => {
    webSockets.close();
    server.close();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.equal(typeof address, "object");

  const pathname = "/v1/processes/flow/io";
  const flow = trackWebSocketReceiveFlow(t, pathname);
  const chunkSize = 600 * 1024;
  const chunk = "x".repeat(chunkSize);
  webSockets.once("connection", (socket) => {
    socket.send(JSON.stringify({
      type: "connected",
      processId: "flow",
      stdinSequence: 0,
      stdoutOffset: chunkSize,
      stderrOffset: chunkSize,
    }));
    socket.send(outputFrame(0x01, 0, chunk));
    socket.send(outputFrame(0x02, 0, chunk));
  });

  const attachment = await managedProcesses({
    baseUrl: `http://127.0.0.1:${address.port}`,
  }).attach("flow", {
    stdinSequence: 0,
    stdoutOffset: 0,
    stderrOffset: 0,
  });
  await attachment.connected;
  await flow.paused;
  assert.equal(flow.pauseCount, 1);

  const stdout = await attachment.stdout[Symbol.asyncIterator]().next();
  assert.equal(stdout.value.byteLength, chunkSize);
  assert.equal(flow.resumeCount, 0);
  const stderr = await attachment.stderr[Symbol.asyncIterator]().next();
  assert.equal(stderr.value.byteLength, chunkSize);
  await flow.resumed;
  assert.equal(flow.resumeCount, 1);
  attachment.close();
});

test("managed process attachment treats stdin acknowledgements as cumulative", { timeout: 5_000 }, async (t) => {
  const server = createServer();
  const webSockets = new WebSocketServer({ server });
  const serverDone = deferred();
  t.after(() => {
    webSockets.close();
    server.close();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.equal(typeof address, "object");

  const receivedSequences = [];
  webSockets.once("connection", (socket) => {
    socket.send(JSON.stringify({
      type: "connected",
      processId: "stdin-ack",
      stdinSequence: 4,
      stdoutOffset: 0,
      stderrOffset: 0,
    }));
    socket.on("message", (raw) => {
      const frame = new Uint8Array(raw.buffer, raw.byteOffset, raw.byteLength);
      receivedSequences.push(Number(new DataView(
        frame.buffer,
        frame.byteOffset + 1,
        8,
      ).getBigUint64(0, false)));
      if (receivedSequences.length === 2) {
        socket.send(JSON.stringify({ type: "stdin_ack", sequence: 6 }));
      } else if (receivedSequences.length === 3) {
        socket.send(JSON.stringify({ type: "stdin_ack", sequence: 5 }));
        serverDone.resolve();
      }
    });
  });

  const attachment = await managedProcesses({
    baseUrl: `http://127.0.0.1:${address.port}`,
  }).attach("stdin-ack", {
    stdinSequence: 4,
    stdoutOffset: 0,
    stderrOffset: 0,
  });
  await attachment.connected;
  await Promise.all([
    attachment.write(5, new Uint8Array([5])),
    attachment.write(6, new Uint8Array([6])),
  ]);
  assert.equal(attachment.stdinSequence, 6);
  await attachment.write(5, new Uint8Array([5]));
  assert.equal(attachment.stdinSequence, 6);
  assert.deepEqual(receivedSequences, [5, 6, 5]);
  await serverDone.promise;
  attachment.close();
});

test("managed process attachment rejects binary data before connected", async (t) => {
  const server = createServer();
  const webSockets = new WebSocketServer({ server });
  t.after(() => {
    webSockets.close();
    server.close();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.equal(typeof address, "object");
  webSockets.once("connection", (socket) => {
    socket.send(outputFrame(0x01, 0, "early"));
  });

  const processes = managedProcesses({
    baseUrl: `http://127.0.0.1:${address.port}`,
  });
  const attachment = await processes.attach("proc", {
    stdinSequence: 0,
    stdoutOffset: 0,
    stderrOffset: 0,
  });
  await assert.rejects(
    attachment.connected,
    /did not begin with connected/,
  );
});

test("managed process attachment rejects discontinuous output offsets", async (t) => {
  const server = createServer();
  const webSockets = new WebSocketServer({ server });
  t.after(() => {
    webSockets.close();
    server.close();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.equal(typeof address, "object");
  webSockets.once("connection", (socket) => {
    socket.send(JSON.stringify({
      type: "connected",
      processId: "proc",
      stdinSequence: 0,
      stdoutOffset: 10,
      stderrOffset: 0,
    }));
    socket.send(outputFrame(0x01, 1, "skipped"));
  });

  const processes = managedProcesses({
    baseUrl: `http://127.0.0.1:${address.port}`,
  });
  const attachment = await processes.attach("proc", {
    stdinSequence: 0,
    stdoutOffset: 0,
    stderrOffset: 0,
  });
  await attachment.connected;
  await assert.rejects(
    attachment.stdout[Symbol.asyncIterator]().next(),
    /offset 1 does not match expected 0/,
  );
});

test("managed process attachment rejects a discontinuous output EOF", async (t) => {
  const server = createServer();
  const webSockets = new WebSocketServer({ server });
  t.after(() => {
    webSockets.close();
    server.close();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.equal(typeof address, "object");
  webSockets.once("connection", (socket) => {
    socket.send(JSON.stringify({
      type: "connected",
      processId: "proc",
      stdinSequence: 0,
      stdoutOffset: 10,
      stderrOffset: 0,
    }));
    socket.send(JSON.stringify({ type: "stdout_eof", offset: 1, clean: true }));
  });

  const processes = managedProcesses({
    baseUrl: `http://127.0.0.1:${address.port}`,
  });
  const attachment = await processes.attach("proc", {
    stdinSequence: 0,
    stdoutOffset: 0,
    stderrOffset: 0,
  });
  await attachment.connected;
  await assert.rejects(
    attachment.stdoutEOF,
    /EOF offset 1 does not match expected 0/,
  );
});

test("managed process attachment requires an explicit EOF clean fact", async (t) => {
  const server = createServer();
  const webSockets = new WebSocketServer({ server });
  t.after(() => {
    webSockets.close();
    server.close();
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert.equal(typeof address, "object");
  webSockets.once("connection", (socket) => {
    socket.send(JSON.stringify({
      type: "connected",
      processId: "proc",
      stdinSequence: 0,
      stdoutOffset: 0,
      stderrOffset: 0,
    }));
    socket.send(JSON.stringify({ type: "stdout_eof", offset: 0 }));
  });

  const processes = managedProcesses({
    baseUrl: `http://127.0.0.1:${address.port}`,
  });
  const attachment = await processes.attach("proc", {
    stdinSequence: 0,
    stdoutOffset: 0,
    stderrOffset: 0,
  });
  await attachment.connected;
  await assert.rejects(
    attachment.stdoutEOF,
    /invalid clean/,
  );
});
