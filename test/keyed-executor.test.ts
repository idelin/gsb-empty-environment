import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ExecutorClosedError,
  JobCancelledError,
  JobHandle,
  KeyedExecutor,
} from '../src/keyed-executor';

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T | PromiseLike<T>) => void;
  reject: (reason?: unknown) => void;
}

function deferred<T = void>(): Deferred<T> {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Flush the microtask queue so all settlement callbacks have run. */
const flush = (): Promise<void> => new Promise((resolve) => setImmediate(resolve));

test('serializes jobs for the same key in FIFO order', async () => {
  const executor = new KeyedExecutor({ concurrency: 8 });
  const events: string[] = [];
  const d1 = deferred<string>();
  const d2 = deferred<string>();
  const d3 = deferred<string>();

  const h1 = executor.submit('a', () => { events.push('start-1'); return d1.promise; });
  const h2 = executor.submit('a', () => { events.push('start-2'); return d2.promise; });
  const h3 = executor.submit('a', () => { events.push('start-3'); return d3.promise; });

  await flush();
  assert.deepEqual(events, ['start-1']);
  assert.equal(h1.state, 'running');
  assert.equal(h2.state, 'queued');
  assert.equal(executor.runningCount, 1);

  d1.resolve('r1');
  await flush();
  assert.deepEqual(events, ['start-1', 'start-2']);
  assert.equal(await h1.promise, 'r1');
  assert.equal(h1.state, 'succeeded');

  d2.resolve('r2');
  await flush();
  assert.deepEqual(events, ['start-1', 'start-2', 'start-3']);

  d3.resolve('r3');
  assert.equal(await h3.promise, 'r3');
  assert.equal(executor.trackedKeyCount, 0, 'idle key must be released');
});

test('shares the concurrency budget across keys and schedules ready keys FIFO', async () => {
  const executor = new KeyedExecutor({ concurrency: 2 });
  const started: string[] = [];
  const gates = new Map<string, Deferred<string>>();
  const start = (key: string) => {
    const gate = deferred<string>();
    gates.set(key, gate);
    return executor.submit(key, () => { started.push(key); return gate.promise; });
  };

  const ha = start('a');
  const hb = start('b');
  const hc = start('c');
  const hd = start('d');

  await flush();
  assert.deepEqual(started, ['a', 'b']);
  assert.equal(executor.runningCount, 2);

  gates.get('a')!.resolve('a-done');
  await flush();
  assert.deepEqual(started, ['a', 'b', 'c'], 'oldest ready key runs first');

  gates.get('b')!.resolve('b-done');
  await flush();
  assert.deepEqual(started, ['a', 'b', 'c', 'd']);

  gates.get('c')!.resolve('c-done');
  gates.get('d')!.resolve('d-done');
  assert.equal(await ha.promise, 'a-done');
  assert.equal(await hb.promise, 'b-done');
  assert.equal(await hc.promise, 'c-done');
  assert.equal(await hd.promise, 'd-done');
  assert.equal(executor.trackedKeyCount, 0);
});

test('a failed job does not block its key and only fails its own handle', async () => {
  const executor = new KeyedExecutor({ concurrency: 1 });
  const events: string[] = [];
  const boom = new Error('boom');

  const h1 = executor.submit('a', () => { events.push('first'); throw boom; });
  const h2 = executor.submit('a', () => { events.push('second'); return 'ok'; });

  await assert.rejects(h1.promise, (error: unknown) => error === boom);
  assert.equal(h1.state, 'failed');
  assert.equal(await h2.promise, 'ok');
  assert.deepEqual(events, ['first', 'second']);
  assert.equal(executor.trackedKeyCount, 0);
});

test('a rejected job does not block its key', async () => {
  const executor = new KeyedExecutor({ concurrency: 1 });
  const d1 = deferred<string>();
  const h1 = executor.submit('a', () => d1.promise);
  const h2 = executor.submit('a', () => 'after-failure');

  d1.reject(new Error('nope'));
  assert.equal(await h2.promise, 'after-failure');
  await assert.rejects(h1.promise, /nope/);
});

test('cancels queued jobs; running jobs are not cancellable', async () => {
  const executor = new KeyedExecutor({ concurrency: 1 });
  const gate = deferred<string>();
  const ran: string[] = [];

  const h1 = executor.submit('a', () => gate.promise);
  const h2 = executor.submit('a', () => { ran.push('two'); return 'two'; });
  const h3 = executor.submit('a', () => { ran.push('three'); return 'three'; });
  const hOther = executor.submit('b', () => { ran.push('other'); return 'other'; });

  assert.equal(h1.cancel(), false, 'running job cannot be cancelled');
  assert.equal(h2.cancel(), true);
  assert.equal(h2.cancel(), false, 'cancel is not repeatable');
  assert.equal(h2.state, 'cancelled');
  await assert.rejects(h2.promise, JobCancelledError);
  assert.equal(executor.hasKey('b'), true, 'key b still queued');

  assert.equal(hOther.cancel(), true);
  assert.equal(executor.hasKey('b'), false, 'cancelling the last queued job releases the key');

  gate.resolve('one');
  assert.equal(await h1.promise, 'one');
  assert.equal(await h3.promise, 'three');
  assert.deepEqual(ran, ['three'], 'cancelled jobs never run');
  assert.equal(executor.trackedKeyCount, 0);
});

test('a job may synchronously submit a follow-up for its own key', async () => {
  const executor = new KeyedExecutor({ concurrency: 4 });
  const order: string[] = [];
  let followUp!: JobHandle<string>;

  const h1 = executor.submit('k', () => {
    order.push('first');
    // Re-entrant submit while the lane is running: must queue behind h1.
    followUp = executor.submit('k', () => {
      order.push('second');
      return 'two';
    });
    return 'one';
  });

  assert.equal(await h1.promise, 'one');
  assert.equal(await followUp.promise, 'two');
  assert.deepEqual(order, ['first', 'second'], 'follow-up runs only after its key frees up');
  assert.equal(executor.trackedKeyCount, 0);
});

test('a task submitted from a settlement callback during lane cleanup is not lost', async () => {
  const executor = new KeyedExecutor({ concurrency: 2 });
  const order: string[] = [];

  const h1 = executor.submit('k', () => { order.push('one'); return '1'; });
  // This callback runs right after the lane for "k" has been released.
  const followUp = h1.promise.then(() =>
    executor.submit('k', () => { order.push('two'); return '2'; }).promise,
  );

  assert.equal(await followUp, '2');
  assert.deepEqual(order, ['one', 'two']);
  assert.equal(executor.hasKey('k'), false, 'no stale lane left behind');
  assert.equal(executor.trackedKeyCount, 0);

  // The executor stays fully usable for the same key afterwards.
  const h3 = executor.submit('k', () => '3');
  assert.equal(await h3.promise, '3');
  assert.equal(executor.trackedKeyCount, 0);
});

test('close cancels queued jobs, waits for running ones, rejects recursive submits', async () => {
  const executor = new KeyedExecutor({ concurrency: 1 });
  const gate = deferred<string>();
  let submitDuringClose!: () => JobHandle<string>;

  const h1 = executor.submit('a', () => {
    submitDuringClose = () => executor.submit('a', () => 'recursive');
    return gate.promise;
  });
  const h2 = executor.submit('a', () => 'queued-a');
  const h3 = executor.submit('b', () => 'queued-b');

  const closing = executor.close();
  assert.equal(executor.isClosed, true);

  await assert.rejects(h2.promise, JobCancelledError);
  await assert.rejects(h3.promise, JobCancelledError);
  assert.equal(h2.state, 'cancelled');
  assert.equal(h3.state, 'cancelled');

  // Recursive submission from the still-running job during shutdown.
  const recursive = submitDuringClose();
  assert.equal(recursive.state, 'failed');
  await assert.rejects(recursive.promise, ExecutorClosedError);

  // External submissions are rejected too.
  await assert.rejects(executor.submit('c', () => 'late').promise, ExecutorClosedError);

  // close() is still pending while h1 runs.
  let settled = false;
  void closing.then(() => { settled = true; });
  await flush();
  assert.equal(settled, false);
  assert.equal(executor.runningCount, 1);

  gate.resolve('done');
  await closing;
  assert.equal(settled, true);
  assert.equal(await h1.promise, 'done');
  assert.equal(executor.runningCount, 0);
  assert.equal(executor.trackedKeyCount, 0);

  assert.equal(executor.close(), closing, 'close is idempotent');
});

test('close on an idle executor resolves immediately', async () => {
  const executor = new KeyedExecutor({ concurrency: 2 });
  await executor.close();
  assert.equal(executor.isClosed, true);
  await assert.rejects(executor.submit('x', () => 1).promise, ExecutorClosedError);
});

test('a key re-joins the ready queue behind already-waiting keys', async () => {
  const executor = new KeyedExecutor({ concurrency: 1 });
  const order: string[] = [];
  const job = (tag: string) => () => { order.push(tag); };

  const h1 = executor.submit('a', job('a1'));
  const h2 = executor.submit('b', job('b1'));
  const h3 = executor.submit('a', job('a2'));
  const h4 = executor.submit('c', job('c1'));

  await Promise.all([h1.promise, h2.promise, h3.promise, h4.promise]);
  // a2 only becomes ready when a1 settles, so it queues behind c1.
  assert.deepEqual(order, ['a1', 'b1', 'c1', 'a2']);
  assert.equal(executor.trackedKeyCount, 0);
});

test('rejects invalid concurrency', () => {
  assert.throws(() => new KeyedExecutor({ concurrency: 0 }), RangeError);
  assert.throws(() => new KeyedExecutor({ concurrency: 1.5 }), RangeError);
});
