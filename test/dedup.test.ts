import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RequestDeduplicator, type ExecuteResult } from '../src/dedup.ts';
import { ManualClock } from '../src/clock.ts';

const RETENTION = 1000;

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function setup() {
  const clock = new ManualClock(10_000);
  const dedup = new RequestDeduplicator({ retentionMs: RETENTION, clock });
  return { clock, dedup };
}

const id = (requestId: string, generation = 1, clientId = 'client-a') => ({
  clientId,
  generation,
  requestId,
});

test('fresh execution runs fn once and returns fresh result', async () => {
  const { dedup } = setup();
  let calls = 0;
  const result = await dedup.execute(id('r1'), async () => {
    calls += 1;
    return 'value';
  });
  assert.deepEqual(result, { kind: 'fresh', value: 'value' });
  assert.equal(calls, 1);
});

test('concurrent duplicate joins the in-flight execution instead of running fn', async () => {
  const { dedup } = setup();
  const d = deferred<string>();
  let firstCalls = 0;
  let secondCalls = 0;

  const p1 = dedup.execute(id('r1'), async () => {
    firstCalls += 1;
    return d.promise;
  });
  const p2 = dedup.execute(id('r1'), async () => {
    secondCalls += 1;
    return 'other';
  });

  d.resolve('shared');
  const [r1, r2] = await Promise.all([p1, p2]);
  assert.deepEqual(r1, { kind: 'fresh', value: 'shared' });
  assert.deepEqual(r2, { kind: 'joined', value: 'shared' });
  assert.equal(firstCalls, 1);
  assert.equal(secondCalls, 0);
});

test('joined waiters observe the same rejection as the first caller', async () => {
  const { dedup } = setup();
  const d = deferred<string>();
  const boom = new Error('boom');

  const p1 = dedup.execute(id('r1'), () => d.promise);
  const p2 = dedup.execute(id('r1'), async () => 'unreachable');
  const a1 = assert.rejects(p1, (err) => err === boom);
  const a2 = assert.rejects(p2, (err) => err === boom);
  d.reject(boom);
  await Promise.all([a1, a2]);
});

test('settled duplicate within retention replays the cached result', async () => {
  const { clock, dedup } = setup();
  let calls = 0;
  const fn = async () => {
    calls += 1;
    return 'cached';
  };

  await dedup.execute(id('r1'), fn);
  clock.advance(RETENTION - 1);
  const result = await dedup.execute(id('r1'), fn);
  assert.deepEqual(result, { kind: 'replayed', value: 'cached' });
  assert.equal(calls, 1);
});

test('record is expired exactly at settledAt + retentionMs and returns indeterminate', async () => {
  const { clock, dedup } = setup();
  let calls = 0;
  const fn = async () => {
    calls += 1;
    return 'x';
  };

  await dedup.execute(id('r1'), fn);
  clock.advance(RETENTION);
  const result = await dedup.execute(id('r1'), fn);
  assert.deepEqual(result, { kind: 'indeterminate', reason: 'expired-record' });
  assert.equal(calls, 1, 'fn must not run again for an expired request');
});

test('evicted generation is retired: same-generation requests become indeterminate, newer generation executes', async () => {
  const { clock, dedup } = setup();
  await dedup.execute(id('r1', 3), async () => 'v1');
  clock.advance(RETENTION);

  const expired = await dedup.execute(id('r1', 3), async () => 'v2');
  assert.equal(expired.kind, 'indeterminate');
  assert.equal(dedup.retiredGeneration('client-a'), 3);

  const sibling = await dedup.execute(id('r2', 3), async () => 'v3');
  assert.deepEqual(sibling, { kind: 'indeterminate', reason: 'retired-generation' });

  const older = await dedup.execute(id('r3', 2), async () => 'v4');
  assert.equal(older.kind, 'indeterminate');

  const newer = await dedup.execute(id('r4', 4), async () => 'v5');
  assert.deepEqual(newer, { kind: 'fresh', value: 'v5' });
});

test('cleanup sweeps expired records and retires their generations', async () => {
  const { clock, dedup } = setup();
  await dedup.execute(id('r1', 1), async () => 'a');
  await dedup.execute(id('r2', 2), async () => 'b');
  assert.equal(dedup.size, 2);

  clock.advance(RETENTION);
  assert.equal(dedup.cleanup(), 2);
  assert.equal(dedup.size, 0);
  assert.equal(dedup.retiredGeneration('client-a'), 2);

  const result = await dedup.execute(id('r1', 1), async () => 'again');
  assert.deepEqual(result, { kind: 'indeterminate', reason: 'retired-generation' });
});

test('cleanup at the exact instant of first completion keeps the fresh result', async () => {
  const { clock, dedup } = setup();
  const d = deferred<string>();

  const p1 = dedup.execute(id('r1', 1), () => d.promise);
  d.resolve('just-in-time');
  const first = await p1;
  assert.deepEqual(first, { kind: 'fresh', value: 'just-in-time' });

  const evicted = dedup.cleanup();
  assert.equal(evicted, 0, 'record completing now must survive a same-instant sweep');
  assert.equal(dedup.size, 1);

  const second = await dedup.execute(id('r1', 1), async () => 'rerun');
  assert.deepEqual(second, { kind: 'replayed', value: 'just-in-time' });
});

test('completion landing on a sweep tick with an already-expired sibling only evicts the sibling', async () => {
  const { clock, dedup } = setup();
  await dedup.execute(id('old', 1), async () => 'old-value');

  clock.advance(RETENTION);
  const d = deferred<string>();
  const p = dedup.execute(id('new', 2), () => d.promise);
  d.resolve('new-value');
  await p;

  assert.equal(dedup.cleanup(), 1, 'only the record expired at this tick is evicted');
  assert.equal(dedup.retiredGeneration('client-a'), 1);

  const replayed = await dedup.execute(id('new', 2), async () => 'rerun');
  assert.deepEqual(replayed, { kind: 'replayed', value: 'new-value' });
});

test('duplicate arriving at the exact expiry tick is indeterminate, one tick earlier replays', async () => {
  const { clock, dedup } = setup();
  await dedup.execute(id('r1', 1), async () => 'v');

  clock.advance(RETENTION - 1);
  const before = await dedup.execute(id('r1', 1), async () => 'rerun');
  assert.deepEqual(before, { kind: 'replayed', value: 'v' });

  clock.advance(1);
  const at = await dedup.execute(id('r1', 1), async () => 'rerun');
  assert.deepEqual(at, { kind: 'indeterminate', reason: 'expired-record' });
});

test('failures are cached within retention and expire into indeterminate', async () => {
  const { clock, dedup } = setup();
  const boom = new Error('boom');
  let calls = 0;
  const fn = async (): Promise<string> => {
    calls += 1;
    throw boom;
  };

  await assert.rejects(dedup.execute(id('r1', 1), fn), (err) => err === boom);
  clock.advance(RETENTION - 1);
  await assert.rejects(dedup.execute(id('r1', 1), fn), (err) => err === boom);
  assert.equal(calls, 1, 'failure replay must not re-run fn');

  clock.advance(1);
  const result = await dedup.execute(id('r1', 1), fn);
  assert.deepEqual(result, { kind: 'indeterminate', reason: 'expired-record' });
  assert.equal(calls, 1);
});

test('records are isolated per client', async () => {
  const { clock, dedup } = setup();
  await dedup.execute(id('r1', 1, 'client-a'), async () => 'a');
  clock.advance(RETENTION);
  dedup.cleanup();

  const other = await dedup.execute(id('r1', 1, 'client-b'), async () => 'b');
  assert.deepEqual(other, { kind: 'fresh', value: 'b' });
  assert.equal(dedup.retiredGeneration('client-b'), -1);
});

test('in-flight records are never evicted by cleanup', async () => {
  const { clock, dedup } = setup();
  const d = deferred<string>();
  const p = dedup.execute(id('r1', 1), () => d.promise);

  clock.advance(RETENTION * 10);
  assert.equal(dedup.cleanup(), 0);

  d.resolve('late');
  const result = await p;
  assert.deepEqual(result, { kind: 'fresh', value: 'late' });
});

test('invalid identities are rejected', async () => {
  const { dedup } = setup();
  const fn = async () => 'v';
  await assert.rejects(dedup.execute(id('r1', -1), fn), TypeError);
  await assert.rejects(dedup.execute(id('r1', 1.5), fn), TypeError);
  await assert.rejects(dedup.execute(id('', 1), fn), TypeError);
  await assert.rejects(dedup.execute(id('r1', 1, ''), fn), TypeError);
});

test('constructor rejects non-positive retention', () => {
  assert.throws(() => new RequestDeduplicator({ retentionMs: 0 }), RangeError);
  assert.throws(() => new RequestDeduplicator({ retentionMs: -5 }), RangeError);
});

test('result kinds are discriminable', async () => {
  const { dedup } = setup();
  const result: ExecuteResult<number> = await dedup.execute(id('r1', 1), async () => 7);
  if (result.kind === 'indeterminate') {
    assert.fail('fresh execution must not be indeterminate');
  }
  assert.equal(result.value, 7);
});
