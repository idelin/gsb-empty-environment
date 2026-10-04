import { RequestCoalescer, CoalescerAbortError, CoalescerTimeoutError } from '../src/coalescer.js';
import type { Loader } from '../src/coalescer.js';

declare const process: { exit(code: number): void };
declare const setImmediate: (fn: () => void) => void;

interface TimerEntry {
  id: number;
  at: number;
  fn: () => void;
}

class ManualScheduler {
  private now = 0;
  private nextId = 1;
  private queue: Array<TimerEntry> = [];

  setTimeout(fn: () => void, ms: number) {
    const id = this.nextId++;
    this.queue.push({ id, at: this.now + ms, fn });
    return () => {
      this.queue = this.queue.filter((entry) => entry.id !== id);
    };
  }

  advanceBy(ms: number): void {
    const target = this.now + ms;
    for (;;) {
      const next = this.takeEarliest(target);
      if (next === undefined) {
        break;
      }
      this.now = next.at;
      next.fn();
    }
    this.now = target;
  }

  private takeEarliest(target: number): TimerEntry | undefined {
    let best = undefined as TimerEntry | undefined;
    for (const entry of this.queue) {
      if (entry.at <= target && (best === undefined || entry.at < best.at)) {
        best = entry;
      }
    }
    if (best !== undefined) {
      const bestId = best.id;
      this.queue = this.queue.filter((entry) => entry.id !== bestId);
    }
    return best;
  }

  get pendingCount(): number {
    return this.queue.length;
  }
}

type ResolveFn<V> = (value: V) => void;
type RejectFn = (error: unknown) => void;

interface Deferred<V> {
  promise: Promise<V>;
  resolve: ResolveFn<V>;
  reject: RejectFn;
}

function deferred<V>(): Deferred<V> {
  let resolve = undefined as ResolveFn<V> | undefined;
  let reject = undefined as RejectFn | undefined;
  const promise = new Promise<V>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve: resolve as ResolveFn<V>, reject: reject as RejectFn };
}

const flush = (): Promise<void> => new Promise<void>((resolve) => setImmediate(resolve));

function assert(cond: boolean, message: string): void {
  if (!cond) {
    throw new Error('assertion failed: ' + message);
  }
}

function assertEqual<T>(actual: T, expected: T, message: string): void {
  if (!Object.is(actual, expected)) {
    throw new Error(message + ' -- expected ' + String(expected) + ', got ' + String(actual));
  }
}

async function assertRejects(
  promise: Promise<unknown>,
  ctor: new (message: string) => Error,
  label: string,
): Promise<Error> {
  try {
    await promise;
  } catch (error) {
    if (!(error instanceof ctor)) {
      throw new Error(label + ': expected rejection to be instance of ' + ctor.name + ', got ' + String(error));
    }
    return error as Error;
  }
  throw new Error(label + ': expected promise to reject');
}

function makeCoalescer(loader: Loader<string, string>, scheduler: ManualScheduler): RequestCoalescer<string, string> {
  return new RequestCoalescer<string, string>({
    loader,
    schedule: (fn: () => void, ms: number) => scheduler.setTimeout(fn, ms),
  });
}

type TestFn = () => void | Promise<void>;

const tests = [] as Array<{ name: string; fn: TestFn }>;

function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

test('coalesces concurrent loads for the same key', async () => {
  const scheduler = new ManualScheduler();
  const d = deferred<string>();
  let calls = 0;
  const coalescer = makeCoalescer(() => {
    calls++;
    return d.promise;
  }, scheduler);
  const p1 = coalescer.load('a');
  const p2 = coalescer.load('a');
  assertEqual(calls, 1, 'loader runs once for two callers');
  d.resolve('value');
  assertEqual(await p1, 'value', 'first caller resolves');
  assertEqual(await p2, 'value', 'second caller resolves');
  assertEqual(coalescer.pendingBatchCount, 0, 'batch cleaned up');
});

test('each caller keeps its own deadline', async () => {
  const scheduler = new ManualScheduler();
  const d = deferred<string>();
  const coalescer = makeCoalescer(() => d.promise, scheduler);
  const p1 = coalescer.load('a', { timeoutMs: 50 });
  const p2 = coalescer.load('a', { timeoutMs: 150 });
  scheduler.advanceBy(50);
  await assertRejects(p1, CoalescerTimeoutError, 'p1 times out at its own deadline');
  assertEqual(coalescer.pendingBatchCount, 1, 'batch still alive for p2');
  d.resolve('late-but-in-time');
  assertEqual(await p2, 'late-but-in-time', 'p2 survives past p1 deadline');
  assertEqual(scheduler.pendingCount, 0, 'p2 timer cleared after settle');
});

test('one caller cancelling does not cancel the others', async () => {
  const scheduler = new ManualScheduler();
  const d = deferred<string>();
  const signals = [] as Array<AbortSignal>;
  const coalescer = makeCoalescer((key, signal) => {
    signals.push(signal);
    return d.promise;
  }, scheduler);
  const ac = new AbortController();
  const p1 = coalescer.load('a', { signal: ac.signal });
  const p2 = coalescer.load('a');
  ac.abort();
  await assertRejects(p1, CoalescerAbortError, 'p1 aborted by its own signal');
  assertEqual(signals[0].aborted, false, 'batch signal not aborted while p2 remains');
  d.resolve('shared');
  assertEqual(await p2, 'shared', 'p2 still receives the value');
});

test('last caller leaving aborts the load and frees the key', async () => {
  const scheduler = new ManualScheduler();
  const deferreds = [] as Array<Deferred<string>>;
  const signals = [] as Array<AbortSignal>;
  const coalescer = makeCoalescer((key, signal) => {
    const d = deferred<string>();
    deferreds.push(d);
    signals.push(signal);
    return d.promise;
  }, scheduler);
  const p1 = coalescer.load('a', { timeoutMs: 10 });
  const p2 = coalescer.load('a', { timeoutMs: 20 });
  scheduler.advanceBy(10);
  await assertRejects(p1, CoalescerTimeoutError, 'p1 times out');
  assertEqual(signals[0].aborted, false, 'not aborted while p2 remains');
  scheduler.advanceBy(10);
  await assertRejects(p2, CoalescerTimeoutError, 'p2 times out');
  assertEqual(signals[0].aborted, true, 'aborted after last caller left');
  assertEqual(coalescer.pendingBatchCount, 0, 'batch removed');
  deferreds[0].resolve('stale');
  await flush();
  const p3 = coalescer.load('a');
  assertEqual(deferreds.length, 2, 'a fresh load starts for the freed key');
  deferreds[1].resolve('fresh');
  assertEqual(await p3, 'fresh', 'new batch resolves with its own result');
});

test('late result from an aborted generation cannot resolve a newer batch', async () => {
  const scheduler = new ManualScheduler();
  const deferreds = [] as Array<Deferred<string>>;
  const coalescer = makeCoalescer(() => {
    const d = deferred<string>();
    deferreds.push(d);
    return d.promise;
  }, scheduler);
  const p1 = coalescer.load('a', { timeoutMs: 10 });
  scheduler.advanceBy(10);
  await assertRejects(p1, CoalescerTimeoutError, 'gen1 caller times out');
  const p2 = coalescer.load('a');
  assertEqual(deferreds.length, 2, 'gen2 started');
  deferreds[0].resolve('stale-value');
  await flush();
  assertEqual(coalescer.pendingBatchCount, 1, 'gen2 still pending after stale resolve');
  deferreds[1].resolve('fresh-value');
  assertEqual(await p2, 'fresh-value', 'gen2 resolves with its own value');
});

test('late rejection from an aborted generation cannot fail a newer batch', async () => {
  const scheduler = new ManualScheduler();
  const deferreds = [] as Array<Deferred<string>>;
  const coalescer = makeCoalescer(() => {
    const d = deferred<string>();
    deferreds.push(d);
    return d.promise;
  }, scheduler);
  const p1 = coalescer.load('a', { timeoutMs: 10 });
  scheduler.advanceBy(10);
  await assertRejects(p1, CoalescerTimeoutError, 'gen1 caller times out');
  const p2 = coalescer.load('a');
  assertEqual(deferreds.length, 2, 'gen2 started');
  deferreds[0].reject(new Error('stale failure'));
  await flush();
  assertEqual(coalescer.pendingBatchCount, 1, 'gen2 still pending after stale rejection');
  deferreds[1].resolve('fresh');
  assertEqual(await p2, 'fresh', 'gen2 resolves normally');
});

test('synchronous loader exception rejects callers and leaves clean state', async () => {
  const scheduler = new ManualScheduler();
  let calls = 0;
  const coalescer = makeCoalescer(() => {
    calls++;
    if (calls === 1) {
      throw new Error('sync-boom');
    }
    return Promise.resolve('recovered');
  }, scheduler);
  const p1 = coalescer.load('a', { timeoutMs: 100 });
  assertEqual(coalescer.pendingBatchCount, 0, 'failed batch removed synchronously');
  assertEqual(scheduler.pendingCount, 0, 'caller timer cleared synchronously');
  const err = await assertRejects(p1, Error, 'p1 rejects with the sync error');
  assertEqual(err.message, 'sync-boom', 'sync error delivered to caller');
  const p2 = coalescer.load('a');
  assertEqual(calls, 2, 'loader retried for a new batch');
  assertEqual(await p2, 'recovered', 'retry succeeds');
});

test('abort callbacks may reentrantly start a new generation', async () => {
  const scheduler = new ManualScheduler();
  const deferreds = [] as Array<Deferred<string>>;
  let reentrant = undefined as Promise<string> | undefined;
  const coalescer = makeCoalescer((key, signal) => {
    const d = deferred<string>();
    deferreds.push(d);
    signal.addEventListener('abort', () => {
      reentrant = coalescer.load('a');
    });
    return d.promise;
  }, scheduler);
  const p1 = coalescer.load('a', { timeoutMs: 10 });
  scheduler.advanceBy(10);
  await assertRejects(p1, CoalescerTimeoutError, 'gen1 caller times out');
  assert(reentrant !== undefined, 'abort callback reentrantly loaded');
  assertEqual(deferreds.length, 2, 'gen2 loader invoked from within abort callback');
  assertEqual(coalescer.pendingBatchCount, 1, 'gen2 is the only pending batch');
  deferreds[0].reject(new Error('gen1 late rejection'));
  await flush();
  assertEqual(coalescer.pendingBatchCount, 1, 'gen2 untouched by gen1 late rejection');
  deferreds[1].resolve('gen2-value');
  assertEqual(await reentrant, 'gen2-value', 'reentrant caller receives gen2 value');
});

test('a pre-aborted caller signal rejects without starting a load', async () => {
  const scheduler = new ManualScheduler();
  let calls = 0;
  const coalescer = makeCoalescer(() => {
    calls++;
    return Promise.resolve('x');
  }, scheduler);
  const ac = new AbortController();
  ac.abort();
  await assertRejects(coalescer.load('a', { signal: ac.signal }), CoalescerAbortError, 'pre-aborted caller rejects');
  assertEqual(calls, 0, 'loader never ran');
  assertEqual(coalescer.pendingBatchCount, 0, 'no batch created');
});

test('different keys load independently', async () => {
  const scheduler = new ManualScheduler();
  let calls = 0;
  const coalescer = makeCoalescer(() => {
    calls++;
    return Promise.resolve('v' + calls);
  }, scheduler);
  const a = coalescer.load('a');
  const b = coalescer.load('b');
  assertEqual(calls, 2, 'one load per key');
  assertEqual(await a, 'v1', 'key a resolves');
  assertEqual(await b, 'v2', 'key b resolves');
});

async function main(): Promise<void> {
  let failed = 0;
  for (const t of tests) {
    try {
      await t.fn();
      console.log('ok   ' + t.name);
    } catch (error) {
      failed++;
      console.error('FAIL ' + t.name);
      console.error(error);
    }
  }
  if (failed > 0) {
    console.error(String(failed) + ' of ' + tests.length + ' tests failed');
    process.exit(1);
  }
  console.log('all ' + tests.length + ' tests passed');
}

main().catch((error: unknown) => {
  console.error(error);
  process.exit(1);
});
