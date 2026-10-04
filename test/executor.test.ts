import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Executor } from '../src/executor.ts';
import type { FailureRecord } from '../src/types.ts';

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test('executes dependencies in order and exposes results', async () => {
  const order: string[] = [];
  const ex = new Executor();
  ex.add('a', () => { order.push('a'); return 1; });
  ex.add('b', () => { order.push('b'); return 2; }, ['a']);
  ex.add('c', () => { order.push('c'); return 3; }, ['b']);
  await ex.run();
  assert.deepEqual(order, ['a', 'b', 'c']);
  assert.equal(ex.result<number>('c'), 3);
  assert.equal(ex.record('a').state, 'completed');
});

test('duplicate edges are deduplicated', async () => {
  const ex = new Executor();
  const a = ex.add('a', () => 'A');
  const b = ex.add('b', () => 'B', [a, a, 'a']);
  ex.dependsOn(b, a);
  ex.dependsOn(b, 'a');
  assert.deepEqual(ex.dependencies(b), [a]);
  await ex.run();
  assert.equal(ex.record(b).state, 'completed');
  assert.equal(ex.result(b), 'B');
});

test('diamond dependencies run the join node exactly once', async () => {
  const events: string[] = [];
  const ex = new Executor();
  ex.add('a', () => { events.push('a'); return 1; });
  ex.add('b', async () => { events.push('b'); return 2; }, ['a']);
  ex.add('c', async () => { events.push('c'); return 3; }, ['a']);
  let dRuns = 0;
  ex.add('d', () => {
    dRuns += 1;
    events.push('d');
    return ex.result<number>('b') + ex.result<number>('c');
  }, ['b', 'c']);
  await ex.run();
  assert.equal(dRuns, 1);
  assert.equal(events[0], 'a');
  assert.deepEqual(new Set(events.slice(1, 3)), new Set(['b', 'c']));
  assert.equal(events[3], 'd');
  assert.equal(ex.result('d'), 5);
});

test('cancelling a running node cancels pending successors but keeps started nodes', async () => {
  const ex = new Executor({ concurrency: 3 });
  const gateB = deferred<string>();
  const gateC = deferred<string>();
  ex.add('root', () => 'R');
  ex.add('b', () => gateB.promise, ['root']);
  ex.add('c', () => gateC.promise, ['root']);
  ex.add('d', () => 'D', ['b']);
  ex.add('e', () => 'E', ['d']);
  const running = ex.run();
  await tick();
  assert.equal(ex.record('b').state, 'running');
  assert.equal(ex.record('c').state, 'running');

  assert.equal(ex.cancel('b', 'no longer needed'), true);
  await tick();

  assert.equal(ex.record('b').state, 'cancelled');
  assert.equal(ex.record('d').state, 'cancelled');
  assert.equal(ex.record('e').state, 'cancelled');
  assert.equal(ex.record('c').state, 'running');

  gateB.resolve('B-late');
  gateC.resolve('C-done');
  await running;
  assert.equal(ex.result('c'), 'C-done');
  assert.equal(ex.record('b').result, undefined);
  assert.equal(ex.record('b').cancellation?.message, 'no longer needed');
});

test('records the cancellation reason chain across propagation', async () => {
  const ex = new Executor();
  ex.add('n0', () => 0);
  ex.add('n1', () => 1, ['n0']);
  ex.add('n2', () => 2, ['n1']);
  ex.add('n3', () => 3, ['n2']);

  assert.equal(ex.cancel('n1', 'user abort'), true);
  await ex.run();

  assert.equal(ex.record('n0').state, 'completed');
  const c1 = ex.record('n1').cancellation!;
  const c2 = ex.record('n2').cancellation!;
  const c3 = ex.record('n3').cancellation!;
  assert.equal(c1.kind, 'explicit');
  assert.equal(c1.message, 'user abort');
  assert.equal(c1.cause, undefined);
  assert.equal(c2.kind, 'dependency');
  assert.deepEqual(c2.sources, [1]);
  assert.equal(c2.primarySource, 1);
  assert.equal(c2.cause, c1);
  assert.equal(c3.kind, 'dependency');
  assert.deepEqual(c3.sources, [2]);
  assert.equal(c3.cause, c2);
});

test('picks the smallest-numbered failed dependency as the primary cause', async () => {
  const ex = new Executor();
  ex.add('x', () => { throw new Error('x failed'); });
  ex.add('y', async () => { throw new Error('y failed'); });
  ex.add('z', () => Promise.reject(new Error('z failed')));
  ex.add('join', () => 'never', ['x', 'y', 'z']);
  await ex.run();

  const rec = ex.record('join');
  assert.equal(rec.state, 'cancelled');
  const cancel = rec.cancellation!;
  assert.equal(cancel.kind, 'dependency');
  assert.deepEqual(cancel.sources, [0, 1, 2]);
  assert.equal(cancel.primarySource, 0);
  const cause = cancel.cause as FailureRecord;
  assert.equal(cause.node, 0);
  assert.equal((cause.error as Error).message, 'x failed');
});

test('failed deps wait for all deps to settle; cancelled deps propagate eagerly', async () => {
  const ex = new Executor();
  const gate = deferred<string>();
  ex.add('slow', () => gate.promise);
  ex.add('boom', () => { throw new Error('boom'); });
  ex.add('join', () => 'never', ['slow', 'boom']);
  const running = ex.run();
  await tick();
  assert.equal(ex.record('boom').state, 'failed');
  assert.equal(ex.record('join').state, 'pending');

  ex.cancel('slow');
  await running;
  const cancel = ex.record('join').cancellation!;
  assert.deepEqual(cancel.sources, [0, 1]);
  assert.equal(cancel.primarySource, 0);
  assert.equal(cancel.cause?.node, 0);
});

test('cancel wins when issued before a pending completion is processed', async () => {
  const ex = new Executor();
  const gate = deferred<string>();
  ex.add('a', () => gate.promise);
  const running = ex.run();
  await tick();
  gate.resolve('too late');
  ex.cancel('a');
  await running;
  assert.equal(ex.record('a').state, 'cancelled');
  assert.equal(ex.record('a').result, undefined);
});

test('completion wins when it settles before cancel', async () => {
  const ex = new Executor();
  const gate = deferred<string>();
  ex.add('a', () => gate.promise);
  const running = ex.run();
  await tick();
  gate.resolve('done');
  await tick();
  assert.equal(ex.cancel('a'), false);
  await running;
  assert.equal(ex.record('a').state, 'completed');
  assert.equal(ex.result('a'), 'done');
});

test('a failing dependency cancels pending dependents, independent branches complete', async () => {
  const ex = new Executor();
  ex.add('bad', () => { throw new Error('nope'); });
  ex.add('child', () => 'never', ['bad']);
  ex.add('independent', () => 'ok');
  await ex.run();
  assert.equal(ex.record('bad').state, 'failed');
  assert.equal((ex.record('bad').failure!.error as Error).message, 'nope');
  const child = ex.record('child');
  assert.equal(child.state, 'cancelled');
  assert.equal(child.cancellation!.kind, 'dependency');
  assert.equal((child.cancellation!.cause as FailureRecord).node, ex.record('bad').id);
  assert.equal(ex.result('independent'), 'ok');
});

test('running tasks receive an AbortSignal that fires on cancel', async () => {
  const ex = new Executor();
  const gate = deferred<string>();
  let observed = false;
  ex.add('a', ({ signal }) => {
    signal.addEventListener('abort', () => { observed = true; });
    return gate.promise;
  });
  const running = ex.run();
  await tick();
  ex.cancel('a');
  assert.equal(observed, true);
  gate.resolve('late');
  await running;
  assert.equal(ex.record('a').state, 'cancelled');
});

test('cancelling a finished node is a no-op', async () => {
  const ex = new Executor();
  ex.add('a', () => 'A');
  ex.add('b', () => 'B', ['a']);
  await ex.run();
  assert.equal(ex.cancel('a'), false);
  assert.equal(ex.record('a').state, 'completed');
  assert.equal(ex.record('b').state, 'completed');
});

test('respects the concurrency limit', async () => {
  const ex = new Executor({ concurrency: 1 });
  let active = 0;
  let maxActive = 0;
  const make = () => async () => {
    active += 1;
    maxActive = Math.max(maxActive, active);
    await tick();
    active -= 1;
  };
  ex.add('t1', make());
  ex.add('t2', make());
  ex.add('t3', make());
  await ex.run();
  assert.equal(maxActive, 1);
});

test('rejects dependency cycles and self dependencies', () => {
  const ex = new Executor();
  ex.add('a', () => 1);
  ex.add('b', () => 2, ['a']);
  assert.throws(() => ex.dependsOn('a', 'b'), /cycle/);
  assert.throws(() => ex.dependsOn('a', 'a'), /cycle/);
});

test('graph cannot be modified after run starts', async () => {
  const ex = new Executor();
  ex.add('a', () => 1);
  const running = ex.run();
  assert.throws(() => ex.add('b', () => 2), /started/);
  assert.throws(() => ex.dependsOn('a', 'a'), /started/);
  await running;
});
