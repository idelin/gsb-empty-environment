import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ZeroAllocCache } from '../src/cache.ts';
import { ObjectPool } from '../src/pool.ts';

test('basic set/get/has/delete and stats', () => {
  const c = new ZeroAllocCache<number>(3);
  assert.equal(c.get('a'), undefined);
  assert.equal(c.stats.misses, 1);

  c.set('a', 1);
  c.set('b', 2);
  assert.equal(c.get('a'), 1);
  assert.equal(c.get('b'), 2);
  assert.equal(c.has('a'), true);
  assert.equal(c.size, 2);
  assert.equal(c.stats.hits, 2);

  assert.equal(c.delete('a'), true);
  assert.equal(c.delete('a'), false, 'second delete is a no-op');
  assert.equal(c.has('a'), false);
  assert.equal(c.size, 1);
});

test('rejects invalid capacity', () => {
  assert.throws(() => new ZeroAllocCache(0), /capacity/);
  assert.throws(() => new ZeroAllocCache(1.5), /capacity/);
});

test('LRU eviction: least-recently-used entry is evicted at capacity', () => {
  const evicted: string[] = [];
  const c = new ZeroAllocCache<number>(2, (k) => evicted.push(k));
  c.set('a', 1);
  c.set('b', 2);
  c.set('c', 3); // evicts 'a'
  assert.deepEqual(evicted, ['a']);
  assert.equal(c.has('a'), false);
  assert.equal(c.get('b'), 2);
  assert.equal(c.get('c'), 3);
  assert.equal(c.size, 2);
  assert.equal(c.stats.evictions, 1);
});

test('get refreshes recency and protects from eviction', () => {
  const c = new ZeroAllocCache<number>(2);
  c.set('a', 1);
  c.set('b', 2);
  assert.equal(c.get('a'), 1); // 'a' now MRU, 'b' is LRU
  c.set('c', 3);               // must evict 'b', not 'a'
  assert.equal(c.has('a'), true);
  assert.equal(c.has('b'), false);
  assert.deepEqual(c.keysMruToLru(), ['c', 'a']);
});

test('overwrite of existing key returns old value, evicts nothing, keeps size', () => {
  const c = new ZeroAllocCache<number>(2);
  c.set('a', 1);
  c.set('b', 2);
  const old = c.set('a', 10);
  assert.equal(old, 1);
  assert.equal(c.size, 2);
  assert.equal(c.stats.evictions, 0);
  assert.equal(c.get('a'), 10);
  assert.deepEqual(c.keysMruToLru(), ['a', 'b'], 'overwrite moves key to MRU');
});

test('capacity-1 cache boundary', () => {
  const c = new ZeroAllocCache<number>(1);
  c.set('a', 1);
  c.set('b', 2); // evicts 'a'
  assert.equal(c.has('a'), false);
  assert.equal(c.get('b'), 2);
  assert.equal(c.size, 1);
  c.delete('b');
  assert.equal(c.size, 0);
  c.set('c', 3);
  assert.equal(c.get('c'), 3);
});

test('delete from the middle keeps LRU order and density consistent', () => {
  const c = new ZeroAllocCache<number>(4);
  c.set('a', 1);
  c.set('b', 2);
  c.set('c', 3);
  c.set('d', 4);
  assert.equal(c.delete('b'), true);
  assert.deepEqual(c.keysMruToLru(), ['d', 'c', 'a']);
  // Refill to capacity: the freed slot must be reusable without corruption.
  c.set('e', 5);
  c.set('f', 6); // evicts LRU ('a')
  assert.equal(c.has('a'), false);
  assert.deepEqual(c.keysMruToLru(), ['f', 'e', 'd', 'c']);
  assert.equal(c.size, 4);
});

test('clear resets all state', () => {
  const c = new ZeroAllocCache<number>(2);
  c.set('a', 1);
  c.set('b', 2);
  c.clear();
  assert.equal(c.size, 0);
  assert.equal(c.has('a'), false);
  c.set('x', 9);
  assert.equal(c.get('x'), 9);
});

test('cache + pool closed loop: evicted values recycle with zero leaks', () => {
  const pool = new ObjectPool<{ v: number }>(
    () => ({ v: 0 }),
    (o) => { o.v = 0; },
    8,
  );
  const cache = new ZeroAllocCache<{ v: number }>(4, (_k, value) => pool.release(value));

  for (let i = 0; i < 10_000; i++) {
    const key = `k${i % 7}`;
    const existed = cache.has(key);
    const obj = pool.acquire();
    obj.v = i;
    const replaced = cache.set(key, obj);
    // Capacity evictions are recycled by onEvict; overwritten values of an
    // existing key come back here and must be released by the caller.
    if (existed && replaced !== undefined) pool.release(replaced);
  }
  // Drain remaining live values out of the cache.
  for (const k of cache.keysMruToLru()) {
    const v = cache.get(k);
    if (v !== undefined) pool.release(v);
  }
  cache.clear();
  pool.assertNoLeaks('cache+pool loop');
  assert.ok(pool.stats.reused > 5000, `values recycled, reused=${pool.stats.reused}`);
});

test('hit path returns identical references and performs no slot churn', () => {
  const c = new ZeroAllocCache<object>(4);
  const val = { n: 1 };
  c.set('hot', val);
  const before = c.stats;
  for (let i = 0; i < 1000; i++) {
    assert.equal(c.get('hot'), val, 'same reference, never copied');
  }
  const after = c.stats;
  assert.equal(after.hits - before.hits, 1000);
  assert.equal(after.evictions, before.evictions);
  assert.equal(after.size, before.size);
});

test('interleaved concurrent ops preserve capacity bound and consistency', async () => {
  const c = new ZeroAllocCache<number>(32);
  const tasks: Promise<void>[] = [];
  for (let i = 0; i < 5000; i++) {
    tasks.push((async () => {
      const key = `k${i % 100}`;
      c.set(key, i);
      if (i % 3 === 0) await Promise.resolve();
      const v = c.get(key);
      if (v !== undefined) assert.equal(typeof v, 'number');
      if (i % 7 === 0) c.delete(`k${(i + 1) % 100}`);
      assert.ok(c.size <= 32, 'capacity bound holds at every interleaving point');
    })());
  }
  await Promise.all(tasks);
  assert.ok(c.size <= 32);
  // Structural consistency: MRU walk visits exactly `size` distinct keys.
  const keys = c.keysMruToLru();
  assert.equal(keys.length, c.size);
  assert.equal(new Set(keys).size, keys.length, 'no duplicate slots in LRU chain');
  for (const k of keys) assert.ok(c.has(k), 'LRU chain and index map agree');
});
