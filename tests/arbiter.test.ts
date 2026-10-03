import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Arbiter, type CommitTicket } from '../src/arbiter.ts';
import { ConflictError } from '../src/errors.ts';

const activeHandles = (): number => (process as unknown as { _getActiveHandles(): unknown[] })._getActiveHandles().length;

test('linearizability: N nodes x M increments, final value and version chain exact', async () => {
  const arbiter = new Arbiter();
  const NODES = 8;
  const PER_NODE = 25;
  const tickets: CommitTicket[] = [];
  await Promise.all(
    Array.from({ length: NODES }, (_, i) =>
      (async () => {
        for (let j = 0; j < PER_NODE; j += 1) {
          tickets.push(
            await arbiter.submitWithRetry<number>(`node-${i}`, 'counter', (c) => (c ?? 0) + 1),
          );
        }
      })(),
    ),
  );
  const total = NODES * PER_NODE;
  const final = await arbiter.read('counter');
  assert.equal(final.value, total);
  assert.equal(final.version, total);

  // Sequence numbers are unique and dense: a total serial order exists.
  const seqs = tickets.map((t) => t.seq).sort((a, b) => a - b);
  assert.deepEqual(seqs, Array.from({ length: total }, (_, i) => i + 1));

  // Replaying commits in seq order yields versions 1..total with no gaps:
  // the observed history is equivalent to a serial execution.
  const bySeq = [...tickets].sort((a, b) => a.seq - b.seq);
  bySeq.forEach((t, i) => assert.equal(t.results[0].version, i + 1));

  const stats = arbiter.stats();
  assert.equal(stats.committed, total);
  assert.equal(stats.pending, 0);
  await arbiter.close();
});

test('conflict arbitration: same expectedVersion -> exactly one winner', async () => {
  const arbiter = new Arbiter();
  const first = await arbiter.compareAndSwap('node-a', 'k', 0, 'a');
  assert.equal(first.results[0].version, 1);

  const results = await Promise.allSettled([
    arbiter.compareAndSwap('node-b', 'k', 1, 'b'),
    arbiter.compareAndSwap('node-c', 'k', 1, 'c'),
    arbiter.compareAndSwap('node-d', 'k', 1, 'd'),
  ]);
  const winners = results.filter((r) => r.status === 'fulfilled');
  const losers = results.filter((r) => r.status === 'rejected');
  assert.equal(winners.length, 1);
  assert.equal(losers.length, 2);
  for (const loser of losers) {
    assert.ok(loser.status === 'rejected');
    assert.ok(loser.reason instanceof ConflictError);
    assert.equal(loser.reason.expectedVersion, 1);
    assert.equal(loser.reason.currentVersion, 2);
  }
  assert.equal((await arbiter.read('k')).version, 2);
  await arbiter.close();
});

test('atomicity: multi-key transaction is all-or-nothing', async () => {
  const arbiter = new Arbiter();
  await arbiter.compareAndSwap('n', 'x', 0, 1);
  await arbiter.compareAndSwap('n', 'y', 0, 1);

  // Stale version on y -> whole transaction must abort, x untouched.
  await assert.rejects(
    arbiter.transact('n', [
      { key: 'x', expectedVersion: 1, value: 100 },
      { key: 'y', expectedVersion: 99, value: 100 },
    ]),
    ConflictError,
  );
  assert.equal((await arbiter.read('x')).value, 1);
  assert.equal((await arbiter.read('y')).value, 1);

  // Valid transaction commits both keys atomically.
  const ticket = await arbiter.transact('n', [
    { key: 'x', expectedVersion: 1, value: 2 },
    { key: 'y', expectedVersion: 1, value: 2 },
  ]);
  assert.deepEqual(
    ticket.results.map((r) => r.version),
    [2, 2],
  );
  assert.equal((await arbiter.read('x')).value, 2);
  assert.equal((await arbiter.read('y')).value, 2);
  await arbiter.close();
});

test('linearizable reads observe enqueue-order state', async () => {
  const arbiter = new Arbiter();
  const w1 = arbiter.compareAndSwap('n', 'k', 0, 'first');
  const mid = arbiter.read('k'); // enqueued between the two writes
  const w2 = arbiter.compareAndSwap('n', 'k', 1, 'second');
  await Promise.all([w1, w2]);
  assert.equal((await mid).value, 'first');
  assert.equal((await arbiter.read('k')).value, 'second');
  await arbiter.close();
});

test('no deadlock: heavy randomized contention completes within watchdog', async () => {
  const arbiter = new Arbiter();
  const KEYS = ['a', 'b', 'c'];
  const work = Promise.all(
    Array.from({ length: 12 }, (_, i) =>
      (async () => {
        for (let j = 0; j < 20; j += 1) {
          const key = KEYS[(i + j) % KEYS.length];
          await arbiter.submitWithRetry<number>(`node-${i}`, key, (c) => (c ?? 0) + 1);
        }
      })(),
    ),
  );
  let timer: ReturnType<typeof setTimeout> | undefined;
  const watchdog = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error('deadlock suspected: watchdog fired')), 10_000);
  });
  try {
    await Promise.race([work, watchdog]);
  } finally {
    clearTimeout(timer); // dispose watchdog so no handle leaks past the test
  }
  const total = KEYS.reduce(
    (sum, key) => sum + ((arbiter.snapshot().get(key)?.value as number) ?? 0),
    0,
  );
  assert.equal(total, 12 * 20);
  await arbiter.close();
});

test('no leaks: operation registry and pending count drain to zero', async () => {
  const handlesBefore = activeHandles();
  const arbiter = new Arbiter();
  await Promise.all(
    Array.from({ length: 50 }, (_, i) =>
      arbiter.submitWithRetry<number>(`node-${i % 5}`, 'leak-key', (c) => (c ?? 0) + 1),
    ),
  );
  await arbiter.drain();
  const stats = arbiter.stats();
  assert.equal(stats.pending, 0);
  assert.equal(stats.liveOps, 0);
  await arbiter.close();
  assert.equal(arbiter.stats().liveOps, 0);
  assert.equal(activeHandles(), handlesBefore);
});

test('closed arbiter rejects new submissions but settles in-flight work', async () => {
  const arbiter = new Arbiter();
  const inflight = arbiter.compareAndSwap('n', 'k', 0, 'v');
  const closed = arbiter.close();
  await assert.rejects(arbiter.compareAndSwap('n', 'k', 1, 'late'), /closed/);
  await inflight;
  await closed;
  assert.equal(arbiter.stats().pending, 0);
});

test('empty transaction is rejected', async () => {
  const arbiter = new Arbiter();
  await assert.rejects(arbiter.transact('n', []), /at least one op/);
  await arbiter.close();
});
