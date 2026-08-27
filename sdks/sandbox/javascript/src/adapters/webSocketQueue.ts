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

import type { RawData } from "ws";

export interface Deferred<T> {
  readonly promise: Promise<T>;
  readonly settled: () => boolean;
  readonly resolve: (value: T) => void;
  readonly reject: (reason: unknown) => void;
}

export function deferred<T>(): Deferred<T> {
  let resolvePromise!: (value: T) => void;
  let rejectPromise!: (reason: unknown) => void;
  let isSettled = false;
  const promise = new Promise<T>((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  return {
    promise,
    settled: () => isSettled,
    resolve(value) {
      if (isSettled) return;
      isSettled = true;
      resolvePromise(value);
    },
    reject(reason) {
      if (isSettled) return;
      isSettled = true;
      rejectPromise(reason);
    },
  };
}

export class AsyncQueue<T> implements AsyncIterable<T> {
  private readonly values: T[] = [];
  private valueHead = 0;
  private readonly waiters: Deferred<IteratorResult<T>>[] = [];
  private ended = false;
  private failure: unknown;

  push(value: T): void {
    if (this.ended || this.failure !== undefined) return;
    const waiter = this.waiters.shift();
    if (waiter) waiter.resolve({ value, done: false });
    else this.values.push(value);
  }

  end(): void {
    if (this.ended || this.failure !== undefined) return;
    this.ended = true;
    for (const waiter of this.waiters.splice(0)) {
      waiter.resolve({ value: undefined, done: true });
    }
  }

  fail(reason: unknown): void {
    if (this.ended || this.failure !== undefined) return;
    this.failure = reason;
    for (const waiter of this.waiters.splice(0)) waiter.reject(reason);
  }

  [Symbol.asyncIterator](): AsyncIterator<T> {
    return {
      next: () => {
        if (this.valueHead < this.values.length) {
          const value = this.values[this.valueHead++]!;
          if (this.valueHead === this.values.length) {
            this.values.length = 0;
            this.valueHead = 0;
          }
          return Promise.resolve({ value, done: false });
        }
        if (this.failure !== undefined) return Promise.reject(this.failure);
        if (this.ended) return Promise.resolve({ value: undefined, done: true });
        const waiter = deferred<IteratorResult<T>>();
        this.waiters.push(waiter);
        return waiter.promise;
      },
    };
  }
}

export function rawDataBytes(data: RawData): Uint8Array {
  if (Array.isArray(data)) {
    const length = data.reduce((sum, part) => sum + part.byteLength, 0);
    const bytes = new Uint8Array(length);
    let offset = 0;
    for (const part of data) {
      bytes.set(part, offset);
      offset += part.byteLength;
    }
    return bytes;
  }
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  return new Uint8Array(data.buffer, data.byteOffset, data.byteLength).slice();
}
