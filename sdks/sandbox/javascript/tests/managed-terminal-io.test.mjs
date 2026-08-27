import assert from "node:assert/strict";
import { createServer } from "node:http";
import test from "node:test";

import { WebSocketServer } from "ws";

import { createExecdClient, ManagedTerminalsAdapter } from "../dist/internal.js";
import { deferred, trackWebSocketReceiveFlow } from "./helpers.mjs";

function managedTerminals(options) {
  const client = createExecdClient(options);
  return new ManagedTerminalsAdapter(client, {
    baseUrl: options.baseUrl,
    headers: options.headers,
  });
}

function outputFrame(offset, text) {
  const data = new TextEncoder().encode(text);
  const frame = new Uint8Array(9 + data.byteLength);
  frame[0] = 0x01;
  new DataView(frame.buffer).setBigUint64(1, BigInt(offset), false);
  frame.set(data, 9);
  return frame;
}

async function listeningServer(t) {
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
  return { webSockets, baseUrl: `http://127.0.0.1:${address.port}` };
}

test("managed terminal attachment preserves input, output, gaps, and resize", async (t) => {
  const { webSockets, baseUrl } = await listeningServer(t);
  const serverDone = deferred();
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
      assert.equal(endpoint.pathname, "/v1/terminals/term%2F1/io");
      assert.deepEqual(Object.fromEntries(endpoint.searchParams), { outputOffset: "3" });

      socket.send(JSON.stringify({
        type: "connected",
        terminalId: "term/1",
        outputOffset: 10,
      }));
      await gapGate;
      socket.send(JSON.stringify({
        type: "gap",
        requestedOffset: 3,
        retainedFrom: 7,
      }));
      await outputGate;
      socket.send(outputFrame(7, "out"));

      let messageNumber = 0;
      socket.on("message", (raw, isBinary) => {
        try {
          messageNumber += 1;
          if (messageNumber === 1) {
            assert.equal(isBinary, true);
            const frame = new Uint8Array(raw.buffer, raw.byteOffset, raw.byteLength);
            assert.equal(frame[0], 0x00);
            assert.equal(new TextDecoder().decode(frame.slice(1)), "input");
            return;
          }
          assert.equal(isBinary, false);
          assert.deepEqual(JSON.parse(raw.toString()), {
            type: "resize",
            rows: 50,
            cols: 140,
          });
          socket.send(JSON.stringify({ type: "output_eof", offset: 10 }));
          socket.send(JSON.stringify({ type: "exit", exitCode: null, signal: "SIGKILL" }));
          socket.close(1000);
          serverDone.resolve();
        } catch (error) {
          serverDone.reject(error);
        }
      });
    } catch (error) {
      serverDone.reject(error);
    }
  });

  const terminals = managedTerminals({
    baseUrl,
    headers: { "x-execd-access-token": "token" },
  });
  const attachment = await terminals.attach("term/1", { outputOffset: 3 });
  assert.deepEqual(await attachment.connected, {
    terminalId: "term/1",
    outputOffset: 10,
  });
  assert.equal(attachment.outputOffset, 3);
  releaseGap();
  assert.deepEqual(await attachment.gaps[Symbol.asyncIterator]().next(), {
    value: { requestedOffset: 3, retainedFrom: 7 },
    done: false,
  });
  assert.equal(attachment.outputOffset, 7);
  releaseOutput();
  assert.equal(new TextDecoder().decode(
    (await attachment.output[Symbol.asyncIterator]().next()).value,
  ), "out");
  assert.equal(attachment.outputOffset, 10);

  await attachment.write(new TextEncoder().encode("input"));
  await attachment.resize(50, 140);
  assert.deepEqual(await attachment.outputEOF, { offset: 10 });
  assert.deepEqual(await attachment.exit, { exitCode: null, signal: "SIGKILL" });
  await serverDone.promise;
});

test("managed terminal attachment applies output backpressure", { timeout: 5_000 }, async (t) => {
  const { webSockets, baseUrl } = await listeningServer(t);
  const flow = trackWebSocketReceiveFlow(t, "/v1/terminals/flow/io");
  const outputSize = 1024 * 1024;
  webSockets.once("connection", (socket) => {
    socket.send(JSON.stringify({
      type: "connected",
      terminalId: "flow",
      outputOffset: outputSize,
    }));
    socket.send(outputFrame(0, "x".repeat(outputSize)));
  });

  const attachment = await managedTerminals({ baseUrl })
    .attach("flow", { outputOffset: 0 });
  await attachment.connected;
  await flow.paused;
  assert.equal(flow.pauseCount, 1);
  const output = await attachment.output[Symbol.asyncIterator]().next();
  assert.equal(output.value.byteLength, outputSize);
  await flow.resumed;
  assert.equal(flow.resumeCount, 1);
  attachment.close();
});

test("managed terminal attachment rejects binary data before connected", async (t) => {
  const { webSockets, baseUrl } = await listeningServer(t);
  webSockets.once("connection", (socket) => socket.send(outputFrame(0, "early")));

  const attachment = await managedTerminals({ baseUrl })
    .attach("term", { outputOffset: 0 });
  await assert.rejects(attachment.connected, /did not begin with connected/);
});

test("managed terminal attachment rejects discontinuous output offsets", async (t) => {
  const { webSockets, baseUrl } = await listeningServer(t);
  webSockets.once("connection", (socket) => {
    socket.send(JSON.stringify({ type: "connected", terminalId: "term", outputOffset: 10 }));
    socket.send(outputFrame(1, "skipped"));
  });

  const attachment = await managedTerminals({ baseUrl })
    .attach("term", { outputOffset: 0 });
  await attachment.connected;
  await assert.rejects(
    attachment.output[Symbol.asyncIterator]().next(),
    /offset 1 does not match expected 0/,
  );
});

test("managed terminal attachment validates resize dimensions before sending", async (t) => {
  const { webSockets, baseUrl } = await listeningServer(t);
  webSockets.once("connection", (socket) => {
    socket.send(JSON.stringify({ type: "connected", terminalId: "term", outputOffset: 0 }));
  });

  const attachment = await managedTerminals({ baseUrl })
    .attach("term", { outputOffset: 0 });
  await attachment.connected;
  await assert.rejects(attachment.resize(0, 80), /rows must be an integer/);
  await assert.rejects(attachment.resize(24, 65_536), /cols must be an integer/);
  attachment.close();
});
