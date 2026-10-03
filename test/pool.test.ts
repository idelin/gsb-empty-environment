import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ObjectPool } from '../src/pool.ts';

test('acquire creates on empty pool, release resets and recycles', () => {
  let resets = 0;
  const pool = new ObjectPool<{ v: number }>(
    () => ({ v: 0 }),
    (o) => {
      o.v = 0;
      resets++;
    },
  );
  const a = pool.acquire();
  a.v = 42;
  pool.release(a);
  assert.equal(resets, 1, 'reset must run on release');
  const b = pool.acquire();
  assert.equal(b, a, 'released object must be recycled');
  assert.equal(b.v, 0, 'state must be reset');
  pool.release(b);
  pool.assertNoLeaks();
});

test('capacity bound: overflow releases are dropped, not retained', () => {
  const pool = new ObjectPool(() => ({}), () => undefined, 2);
  const objs = [pool.acquire(), pool.acquire(), pool.acquire(), pool.acquire()];
  for (const o of objs) pool.release(o);
  assert.equal(pool.size, 2);
  assert.equal(pool.stats.dropped, 2);
  pool.assertNoLeaks();
});

test('zero-capacity pool drops everything but still accounts', () => {
  const pool = new ObjectPool(() => ({}), () => undefined, 0);
  const o = pool.acquire();
  pool.release(o);
  assert.equal(pool.size, 0);
  assert.equal(pool.stats.dropped, 1);
  pool.assertNoLeaks();
});

test('double release throws', () => {
  const pool = new ObjectPool(() => ({}), () => undefined);
  const o = pool.acquire();
  pool.release(o);
  assert.throws(() => pool.release(o), /double release/);
  pool.assertNoLeaks();
});

test('negative capacity rejected', () => {
  assert.throws(() => new ObjectPool(() => ({}), () => undefined, -1));
});

test('assertNoLeaks detects outstanding objects and clears after release', () => {
  const pool = new ObjectPool(() => ({}), () => undefined);
  const a = pool.acquire();
  const b = pool.acquire();
  assert.throws(() => pool.assertNoLeaks(), /leak detected, 2 object/);
  pool.release(a);
  assert.throws(() => pool.assertNoLeaks(), /leak detected, 1 object/);
  pool.release(b);
  pool.assertNoLeaks();
});

test('stats track created/reused/dropped/outstanding', () => {
  const pool = new ObjectPool(() => ({}), () => undefined, 1);
  const a = pool.acquire();
  const b = pool.acquire();
  pool.release(a);
  pool.release(b); // dropped: capacity 1
  const c = pool.acquire(); // reuses a
  assert.equal(c, a);
  const stats = pool.stats;
  assert.equal(stats.created, 2);
  assert.equal(stats.reused, 1);
  assert.equal(stats.dropped, 1);
  assert.equal(stats.outstanding, 1);
  assert.equal(stats.pooled, 0);
  pool.release(c);
  pool.assertNoLeaks();
});

test('stress: 1M acquire/release cycles stay bounded and leak-free', () => {
  const pool = new ObjectPool<number[]>(() => [], (arr) => { arr.length = 0; }, 64);
  for (let i = 0; i < 1_000_000; i++) {
    const arr = pool.acquire();
    arr.push(i);
    pool.release(arr);
  }
  pool.assertNoLeaks();
  assert.ok(pool.size <= 64);
  assert.ok(pool.stats.created <= 65, `created=${pool.stats.created} should stay tiny`);
});
