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
  const paused = deferred();
  const resumed = deferred();
  let pauseCount = 0;
  let resumeCount = 0;

  WebSocket.prototype.pause = function () {
    if (this.url && new URL(this.url).pathname === pathname) {
      pauseCount += 1;
      paused.resolve();
    }
    return originalPause.call(this);
  };
  WebSocket.prototype.resume = function () {
    if (this.url && new URL(this.url).pathname === pathname) {
      resumeCount += 1;
      resumed.resolve();
    }
    return originalResume.call(this);
  };
  t.after(() => {
    WebSocket.prototype.pause = originalPause;
    WebSocket.prototype.resume = originalResume;
  });

  return {
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
