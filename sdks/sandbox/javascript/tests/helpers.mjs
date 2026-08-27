import WebSocket from "ws";

export function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

export function trackWebSocketReceiveFlow(t, pathname) {
  const originalPause = WebSocket.prototype.pause;
  const originalResume = WebSocket.prototype.resume;
  const originalClose = WebSocket.prototype.close;
  const paused = deferred();
  const resumed = deferred();
  const actions = [];
  let pauseCount = 0;
  let resumeCount = 0;

  WebSocket.prototype.pause = function () {
    if (this.url && new URL(this.url).pathname === pathname) {
      pauseCount += 1;
      actions.push("pause");
      paused.resolve();
    }
    return originalPause.call(this);
  };
  WebSocket.prototype.resume = function () {
    if (this.url && new URL(this.url).pathname === pathname) {
      resumeCount += 1;
      actions.push("resume");
      resumed.resolve();
    }
    return originalResume.call(this);
  };
  WebSocket.prototype.close = function (code, data) {
    if (this.url && new URL(this.url).pathname === pathname) {
      actions.push("close");
    }
    return originalClose.call(this, code, data);
  };
  t.after(() => {
    WebSocket.prototype.pause = originalPause;
    WebSocket.prototype.resume = originalResume;
    WebSocket.prototype.close = originalClose;
  });

  return {
    actions,
    paused: paused.promise,
    resumed: resumed.promise,
    get pauseCount() {
      return pauseCount;
    },
    get resumeCount() {
      return resumeCount;
    },
  };
}
