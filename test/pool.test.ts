import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ObjectPool } from '../src/pool.ts';

interface Item {
  id: number;
  payload: string;
}

function makeItemPool(capacity = 4, preallocate = 0) {
  let seq = 0;
  const resetCalls: Item[] = [];
  const pool = new ObjectPool<Item>(
    () => ({ id: seq++, payload: 'x' }),
    (o) => {
      o.payload = '';
      resetCalls.push(o);
    },
    capacity,
    preallocate,
  );
  return { pool, resetCalls };
}

test('acquire creates on demand and release resets + recycles identity', () => {
  const { pool, resetCalls } = makeItemPool();
  const a = pool.acquire();
  a.payload = 'dirty';
  assert.equal(pool.stats.created, 1);
  assert.equal(pool.stats.outstanding, 1);

  pool.release(a);
  assert.equal(resetCalls.length, 1);
  assert.equal(a.payload, '', 'reset must run on release');
  assert.equal(pool.stats.outstanding, 0);

  const b = pool.acquire();
  assert.equal(b, a, 'warm acquire must recycle the same object (zero alloc)');
  assert.equal(pool.stats.created, 1, 'no new construction after warmup');
  assert.equal(pool.stats.reused, 1);
  pool.release(b);
  pool.assertNoLeaks();
});

test('preallocate fills the free stack without acquire', () => {
  const { pool } = makeItemPool(8, 8);
  assert.equal(pool.stats.created, 8);
  assert.equal(pool.stats.freeCount, 8);
  const objs = [pool.acquire(), pool.acquire(), pool.acquire()];
  assert.equal(pool.stats.created, 8, 'all acquires served from preallocated stock');
  assert.equal(pool.stats.reused, 3);
  for (const o of objs) pool.release(o);
  pool.assertNoLeaks();
});

test('double release throws', () => {
  const { pool } = makeItemPool();
  const a = pool.acquire();
  pool.release(a);
  assert.throws(() => pool.release(a), /double release/);
  pool.assertNoLeaks();
});

test('releasing a foreign object throws', () => {
  const { pool } = makeItemPool();
  assert.throws(() => pool.release({ id: 999, payload: '' }), /foreign object/);
});

test('capacity overflow drops objects instead of growing the free stack', () => {
  const { pool } = makeItemPool(2);
  const objs = [pool.acquire(), pool.acquire(), pool.acquire(), pool.acquire()];
  for (const o of objs) pool.release(o);
  const s = pool.stats;
  assert.equal(s.freeCount, 2, 'free stack capped at capacity');
  assert.equal(s.dropped, 2, 'surplus releases are dropped to the GC');
  pool.assertNoLeaks();
});

test('assertNoLeaks throws while objects are checked out', () => {
  const { pool } = makeItemPool();
  const a = pool.acquire();
  assert.throws(() => pool.assertNoLeaks('ctx'), /ctx: leak detected, 1/);
  pool.release(a);
  pool.assertNoLeaks();
});

test('rejects invalid capacity', () => {
  assert.throws(() => new ObjectPool(() => ({}), () => {}, 0), /capacity/);
});

test('interleaved concurrent acquire/release never leaks and bounds creation', async () => {
  const { pool } = makeItemPool(16);
  let next = 0;
  const worker = async (): Promise<void> => {
    while (next < 2000) {
      const i = next++;
      const o = pool.acquire();
      // Yield a varying number of microtask turns to force interleaving.
      for (let y = 0; y < i % 3; y++) await Promise.resolve();
      o.payload = `task-${i}`;
      pool.release(o);
    }
  };
  // 8 concurrent workers, each holding at most one object at a time.
  await Promise.all(Array.from({ length: 8 }, () => worker()));
  const s = pool.stats;
  assert.equal(s.outstanding, 0, 'no outstanding objects after concurrent run');
  assert.ok(s.created <= 8, `creation bounded by worker concurrency, got ${s.created}`);
  assert.ok(s.reused >= 1990, `hot path served from pool, reused=${s.reused}`);
  pool.assertNoLeaks();
});

test('heavy reuse keeps factory calls bounded (GC pressure eliminated)', () => {
  const { pool } = makeItemPool(4, 4);
  for (let i = 0; i < 100_000; i++) {
    const o = pool.acquire();
    o.payload = String(i);
    pool.release(o);
  }
  assert.equal(pool.stats.created, 4, '100k ops constructed only the preallocated 4');
  pool.assertNoLeaks();
});
