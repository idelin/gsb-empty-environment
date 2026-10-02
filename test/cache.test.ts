import { test } from "node:test";
import assert from "node:assert/strict";
import { ZeroAllocCache } from "../src/cache.ts";
import { ObjectPool } from "../src/pool.ts";

test("capacity must be a positive power of two", () => {
  for (const bad of [0, -1, 3, 5, 6, 7, 1000]) {
    assert.throws(() => new ZeroAllocCache(bad), RangeError, `capacity ${bad}`);
  }
  for (const good of [1, 2, 4, 8, 1024]) {
    assert.ok(new ZeroAllocCache(good));
  }
});

test("get/put hit and miss paths", () => {
  const c = new ZeroAllocCache<string>(8);
  assert.equal(c.get(1), undefined);
  assert.equal(c.misses, 1);
  c.put(1, "a");
  assert.equal(c.get(1), "a");
  assert.equal(c.hits, 1);
  assert.equal(c.size, 1);
});

test("direct-mapped collision evicts in place and reports via onEvict", () => {
  const evicted: string[] = [];
  const c = new ZeroAllocCache<string>(4, (v) => evicted.push(v));
  c.put(0, "zero");
  c.put(4, "four"); // 4 & 3 === 0 → collides with key 0
  assert.equal(c.get(0), undefined, "colliding key evicted");
  assert.deepEqual(evicted, ["zero"]);
  assert.equal(c.evictions, 1);
  assert.equal(c.get(4), "four");
  assert.equal(c.size, 1);
});

test("overwriting the same key releases the previous value, no eviction counted", () => {
  const evicted: number[] = [];
  const c = new ZeroAllocCache<number>(4, (v) => evicted.push(v));
  c.put(2, 10);
  c.put(2, 20);
  assert.deepEqual(evicted, [10]);
  assert.equal(c.evictions, 0);
  assert.equal(c.size, 1);
  assert.equal(c.get(2), 20);
});

test("overwriting a key with itself does not self-release", () => {
  const evicted: object[] = [];
  const c = new ZeroAllocCache<object>(4, (v) => evicted.push(v));
  const obj = {};
  c.put(1, obj);
  c.put(1, obj);
  assert.equal(evicted.length, 0);
});

test("delete removes only the exact key and releases its value", () => {
  const evicted: string[] = [];
  const c = new ZeroAllocCache<string>(4, (v) => evicted.push(v));
  c.put(1, "a");
  assert.equal(c.delete(2), false);
  assert.equal(c.delete(1), true);
  assert.deepEqual(evicted, ["a"]);
  assert.equal(c.size, 0);
  assert.equal(c.get(1), undefined);
});

test("clear releases every live value", () => {
  const evicted: number[] = [];
  const c = new ZeroAllocCache<number>(8, (v) => evicted.push(v));
  for (let k = 0; k < 8; k++) c.put(k, k * 10);
  c.clear();
  assert.equal(c.size, 0);
  assert.equal(evicted.length, 8);
  assert.equal(c.get(3), undefined);
});

test("integrated with ObjectPool: evicted entries are recycled, nothing leaks", () => {
  let created = 0;
  const pool = new ObjectPool<{ v: number }>({
    capacity: 16,
    factory: () => ({ v: (created++, 0) }),
    reset: (o) => {
      o.v = 0;
    },
  });
  const cache = new ZeroAllocCache<{ v: number }>(16, (o) => pool.release(o));
  // Insert 4x capacity → every earlier entry evicted back into the pool.
  for (let k = 0; k < 64; k++) {
    const o = pool.acquire();
    o.v = k;
    cache.put(k, o);
  }
  assert.equal(cache.size, 16);
  assert.equal(pool.outstanding, 16, "exactly the live cache entries are checked out");
  cache.clear();
  assert.equal(pool.outstanding, 0, "strict no-leak: everything returned");
  assert.ok(pool.totalReused > 0, "pool reused recycled entries");
});

test("zero-allocation hot path: bounded heap growth over 200k ops", () => {
  const gc = (globalThis as { gc?: () => void }).gc;
  if (gc === undefined) return; // run with --expose-gc to enable
  const cache = new ZeroAllocCache<object>(1024);
  const values = Array.from({ length: 2048 }, () => ({}));
  for (let k = 0; k < 4096; k++) cache.put(k & 2047, values[k & 2047]); // warm-up
  gc();
  const before = process.memoryUsage().heapUsed;
  let sink: object | undefined;
  for (let k = 0; k < 200_000; k++) {
    cache.put(k & 2047, values[k & 2047]);
    sink = cache.get(k & 2047);
  }
  gc();
  const growth = process.memoryUsage().heapUsed - before;
  assert.ok(sink !== undefined);
  assert.ok(
    growth < 256 * 1024,
    `hot path grew heap by ${growth} bytes — expected near-zero allocation`
  );
});

test("high-churn workload keeps counters consistent", () => {
  const c = new ZeroAllocCache<number>(64);
  for (let k = 0; k < 100_000; k++) {
    c.put(k % 500, k);
    c.get((k * 7) % 500);
  }
  assert.equal(c.hits + c.misses, 100_000);
  assert.ok(c.size <= 64);
});
