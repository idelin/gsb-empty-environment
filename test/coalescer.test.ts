import { test } from "node:test";
import assert from "node:assert/strict";
import {
  RequestCoalescer,
  TimeoutError,
  CoalescerAbortError,
} from "../src/coalescer.ts";
import { ManualScheduler } from "../src/scheduler.ts";

/** Let queued promise continuations run without advancing fake time. */
async function flush(turns = 5): Promise<void> {
  for (let i = 0; i < turns; i++) {
    await new Promise<void>((resolve) => setImmediate(resolve));
  }
}

interface Deferred<V> {
  promise: Promise<V>;
  resolve: (value: V) => void;
  reject: (error: unknown) => void;
}

function deferred<V>(): Deferred<V> {
  let resolve!: (value: V) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<V>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Records the settled state of a promise for synchronous assertions. */
function tracked<V>(promise: Promise<V>): {
  promise: Promise<V>;
  state: () => "pending" | "fulfilled" | "rejected";
  value: () => V;
  error: () => unknown;
} {
  let state: "pending" | "fulfilled" | "rejected" = "pending";
  let settledValue: V | undefined;
  let settledError: unknown;
  const wrapped = promise.then(
    (value) => {
      state = "fulfilled";
      settledValue = value;
      return value;
    },
    (error) => {
      state = "rejected";
      settledError = error;
      throw error;
    },
  );
  wrapped.catch(() => {});
  return {
    promise: wrapped,
    state: () => state,
    value: () => settledValue as V,
    error: () => settledError,
  };
}

test("same key loads only once while in flight", async () => {
  const scheduler = new ManualScheduler();
  const loads: string[] = [];
  const pending = deferred<string>();
  const coalescer = new RequestCoalescer<string, string>((key) => {
    loads.push(key);
    return pending.promise;
  }, scheduler);

  const first = tracked(coalescer.request("a"));
  const second = tracked(coalescer.request("a"));
  assert.equal(loads.length, 1);

  pending.resolve("value-a");
  await flush();
  assert.equal(first.state(), "fulfilled");
  assert.equal(second.state(), "fulfilled");
  assert.equal(first.value(), "value-a");
  assert.equal(second.value(), "value-a");
});

test("each caller keeps its own deadline; one leaving does not cancel others", async () => {
  const scheduler = new ManualScheduler();
  const pending = deferred<string>();
  let signalAborted = false;
  const coalescer = new RequestCoalescer<string, string>((_key, signal) => {
    signal.addEventListener("abort", () => {
      signalAborted = true;
    });
    return pending.promise;
  }, scheduler);

  const impatient = tracked(coalescer.request("k", { deadlineMs: 100 }));
  const patient = tracked(coalescer.request("k", { deadlineMs: 5000 }));

  scheduler.advance(100);
  await flush();
  assert.equal(impatient.state(), "rejected");
  assert.ok(impatient.error() instanceof TimeoutError);
  assert.equal(patient.state(), "pending");
  assert.equal(signalAborted, false, "shared load must survive one caller leaving");

  pending.resolve("late-but-welcome");
  await flush();
  assert.equal(patient.state(), "fulfilled");
  assert.equal(patient.value(), "late-but-welcome");
});

test("last caller leaving aborts the load", async () => {
  const scheduler = new ManualScheduler();
  let aborted = false;
  const coalescer = new RequestCoalescer<string, string>((_key, signal) => {
    signal.addEventListener("abort", () => {
      aborted = true;
    });
    return new Promise<string>(() => {});
  }, scheduler);

  const only = tracked(coalescer.request("k", { deadlineMs: 50 }));
  scheduler.advance(50);
  await flush();
  assert.equal(only.state(), "rejected");
  assert.ok(only.error() instanceof TimeoutError);
  assert.equal(aborted, true);
  assert.equal(coalescer.inFlightCount, 0);
});

test("caller AbortSignal cancels only that caller", async () => {
  const scheduler = new ManualScheduler();
  const pending = deferred<string>();
  const coalescer = new RequestCoalescer<string, string>(() => pending.promise, scheduler);

  const controller = new AbortController();
  const leaving = tracked(coalescer.request("k", { signal: controller.signal }));
  const staying = tracked(coalescer.request("k"));

  controller.abort();
  await flush();
  assert.equal(leaving.state(), "rejected");
  assert.ok(leaving.error() instanceof CoalescerAbortError);
  assert.equal(staying.state(), "pending");

  pending.resolve("still-there");
  await flush();
  assert.equal(staying.state(), "fulfilled");
});

test("pre-aborted caller signal rejects without starting a load", async () => {
  const scheduler = new ManualScheduler();
  let loadCount = 0;
  const coalescer = new RequestCoalescer<string, string>(() => {
    loadCount++;
    return new Promise<string>(() => {});
  }, scheduler);

  const controller = new AbortController();
  controller.abort();
  const result = tracked(coalescer.request("k", { signal: controller.signal }));
  await flush();
  assert.equal(result.state(), "rejected");
  assert.equal(loadCount, 0);
  assert.equal(coalescer.inFlightCount, 0);
});

test("late result from an aborted batch cannot resolve a newer batch", async () => {
  const scheduler = new ManualScheduler();
  const loads: Array<Deferred<string>> = [];
  const coalescer = new RequestCoalescer<string, string>(() => {
    const d = deferred<string>();
    loads.push(d);
    return d.promise;
  }, scheduler);

  // Batch 1: caller gives up, load is aborted.
  const first = tracked(coalescer.request("k", { deadlineMs: 100 }));
  scheduler.advance(100);
  await flush();
  assert.equal(first.state(), "rejected");
  assert.equal(coalescer.inFlightCount, 0);

  // Batch 2: a fresh request starts a new load for the same key.
  const second = tracked(coalescer.request("k"));
  assert.equal(loads.length, 2);

  // The stale load finally resolves; it must not touch batch 2.
  loads[0]!.resolve("stale");
  await flush();
  assert.equal(second.state(), "pending");

  loads[1]!.resolve("fresh");
  await flush();
  assert.equal(second.state(), "fulfilled");
  assert.equal(second.value(), "fresh");
});

test("synchronous loader exception rejects callers asynchronously and allows retry", async () => {
  const scheduler = new ManualScheduler();
  let calls = 0;
  const boom = new Error("sync boom");
  const coalescer = new RequestCoalescer<string, string>(() => {
    calls++;
    if (calls === 1) throw boom;
    return Promise.resolve("recovered");
  }, scheduler);

  const first = tracked(coalescer.request("k"));
  assert.equal(first.state(), "pending", "sync throw surfaces as async rejection");
  await flush();
  assert.equal(first.state(), "rejected");
  assert.equal(first.error(), boom);
  assert.equal(coalescer.inFlightCount, 0);

  const second = tracked(coalescer.request("k"));
  await flush();
  assert.equal(second.state(), "fulfilled");
  assert.equal(second.value(), "recovered");
  assert.equal(calls, 2);
});

test("synchronous loader exception rejects every coalesced caller", async () => {
  const scheduler = new ManualScheduler();
  const boom = new Error("sync boom");
  const coalescer = new RequestCoalescer<string, string>(() => {
    throw boom;
  }, scheduler);

  const first = tracked(coalescer.request("k"));
  const second = tracked(coalescer.request("k"));
  await flush();
  assert.equal(first.state(), "rejected");
  assert.equal(second.state(), "rejected");
  assert.equal(first.error(), boom);
  assert.equal(second.error(), boom);
});

test("abort listener may re-enter request() and starts a fresh batch", async () => {
  const scheduler = new ManualScheduler();
  const loads: Array<Deferred<string>> = [];
  let reentrantResult: ReturnType<typeof tracked<string>> | null = null;

  const coalescer = new RequestCoalescer<string, string>((_key, signal) => {
    const d = deferred<string>();
    loads.push(d);
    signal.addEventListener("abort", () => {
      // Re-entrant call from inside the cancellation callback.
      reentrantResult = tracked(coalescer.request("k"));
    });
    return d.promise;
  }, scheduler);

  const original = tracked(coalescer.request("k", { deadlineMs: 100 }));
  scheduler.advance(100); // times out -> teardown -> abort -> re-entrant request
  await flush();

  assert.equal(original.state(), "rejected");
  assert.ok(reentrantResult !== null, "abort listener re-entered request()");
  assert.equal(loads.length, 2, "re-entrant request started a new load");
  assert.equal(reentrantResult!.state(), "pending");
  assert.equal(coalescer.inFlightCount, 1);

  // The aborted load resolving late must not disturb the re-entrant batch.
  loads[0]!.resolve("stale");
  await flush();
  assert.equal(reentrantResult!.state(), "pending");

  loads[1]!.resolve("reentrant-fresh");
  await flush();
  assert.equal(reentrantResult!.state(), "fulfilled");
  assert.equal(reentrantResult!.value(), "reentrant-fresh");
  assert.equal(coalescer.inFlightCount, 0);
});

test("deadline timer is cancelled once the batch settles", async () => {
  const scheduler = new ManualScheduler();
  const pending = deferred<string>();
  const coalescer = new RequestCoalescer<string, string>(() => pending.promise, scheduler);

  const result = tracked(coalescer.request("k", { deadlineMs: 1000 }));
  pending.resolve("done");
  await flush();
  assert.equal(result.state(), "fulfilled");
  assert.equal(scheduler.pendingCount, 0, "no dangling deadline timers");

  scheduler.advance(5000);
  await flush();
  assert.equal(result.state(), "fulfilled");
});

test("loader rejection rejects all participants and frees the key", async () => {
  const scheduler = new ManualScheduler();
  const pending = deferred<string>();
  const coalescer = new RequestCoalescer<string, string>(() => pending.promise, scheduler);

  const first = tracked(coalescer.request("k"));
  const second = tracked(coalescer.request("k"));
  const boom = new Error("load failed");
  pending.reject(boom);
  await flush();

  assert.equal(first.state(), "rejected");
  assert.equal(second.state(), "rejected");
  assert.equal(first.error(), boom);
  assert.equal(coalescer.inFlightCount, 0);
});
