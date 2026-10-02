import { test } from "node:test";
import assert from "node:assert/strict";
import { ObjectPool } from "../src/pool.ts";

interface Item {
  id: number;
  dirty: number;
}

function makePool(overrides: Partial<Parameters<typeof ObjectPool<Item>>[0]> = {}) {
  let nextId = 0;
  const resetCalls: Item[] = [];
  const pool = new ObjectPool<Item>({
    name: "test",
    capacity: 4,
    factory: () => ({ id: nextId++, dirty: 0 }),
    reset: (o) => {
      o.dirty = 0;
      resetCalls.push(o);
    },
    ...overrides,
  });
  return { pool, resetCalls, created: () => nextId };
}

test("constructor rejects invalid capacity and prealloc", () => {
  assert.throws(() => new ObjectPool({ capacity: 0, factory: () => ({}), reset: () => {} }), RangeError);
  assert.throws(() => new ObjectPool({ capacity: -3, factory: () => ({}), reset: () => {} }), RangeError);
  assert.throws(() => new ObjectPool({ capacity: 2.5, factory: () => ({}), reset: () => {} }), RangeError);
  assert.throws(
    () => new ObjectPool({ capacity: 2, prealloc: 5, factory: () => ({}), reset: () => {} }),
    RangeError
  );
});

test("prealloc fills the freelist without acquire", () => {
  const { pool } = makePool({ prealloc: 3 });
  assert.equal(pool.idleCount, 3);
  assert.equal(pool.totalCreated, 3);
  assert.equal(pool.outstanding, 0);
});

test("acquire reuses released objects — zero new allocations after warm-up", () => {
  const { pool } = makePool();
  const a = pool.acquire();
  pool.release(a);
  const createdBefore = pool.totalCreated;
  const reusedBefore = pool.totalReused;
  for (let i = 0; i < 10_000; i++) {
    const o = pool.acquire();
    pool.release(o);
  }
  assert.equal(pool.totalCreated, createdBefore, "hot path must not create new objects");
  assert.equal(pool.totalReused - reusedBefore, 10_000);
  assert.equal(pool.outstanding, 0);
});

test("release resets object state before caching", () => {
  const { pool, resetCalls } = makePool();
  const o = pool.acquire();
  o.dirty = 99;
  pool.release(o);
  assert.equal(resetCalls.length, 1);
  assert.equal(o.dirty, 0, "reset must run before the object is cached");
  assert.equal(pool.acquire(), o);
});

test("double release throws", () => {
  const { pool } = makePool();
  const o = pool.acquire();
  pool.release(o);
  assert.throws(() => pool.release(o), /already-released|unknown/);
});

test("releasing a foreign object throws", () => {
  const { pool } = makePool();
  assert.throws(() => pool.release({ id: 999, dirty: 0 }), /unknown|already-released/);
});

test("maxOutstanding is a hard cap", () => {
  const { pool } = makePool({ maxOutstanding: 2, capacity: 1, prealloc: 0 });
  pool.acquire();
  pool.acquire();
  assert.throws(() => pool.acquire(), /exhausted/);
});

test("freelist is bounded by capacity; overflow is dropped by design", () => {
  const { pool } = makePool({ capacity: 2, prealloc: 0 });
  const objs = [pool.acquire(), pool.acquire(), pool.acquire(), pool.acquire()];
  for (const o of objs) pool.release(o);
  assert.equal(pool.idleCount, 2, "freelist never exceeds capacity");
  assert.equal(pool.outstanding, 0);
});

test("concurrent interleaved acquire/release never hands out a live object twice", async () => {
  const { pool } = makePool({ capacity: 8 });
  const inUse = new Set<Item>();
  const workers = Array.from({ length: 64 }, async () => {
    for (let i = 0; i < 300; i++) {
      const o = pool.acquire();
      assert.ok(!inUse.has(o), "object handed out while still checked out");
      inUse.add(o);
      await Promise.resolve(); // interleave workers
      assert.ok(inUse.delete(o));
      pool.release(o);
    }
  });
  await Promise.all(workers);
  assert.equal(pool.outstanding, 0, "no leaks after concurrent workload");
  assert.equal(pool.totalAcquires, pool.totalReleases);
});

test("random workload leaves zero leaks and consistent counters", () => {
  const { pool } = makePool({ capacity: 16 });
  const live: Item[] = [];
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let i = 0; i < 20_000; i++) {
    if (live.length === 0 || (rand() < 0.55 && live.length < 32)) {
      live.push(pool.acquire());
    } else {
      const idx = Math.floor(rand() * live.length);
      pool.release(live.splice(idx, 1)[0]);
    }
    assert.equal(pool.outstanding, live.length);
  }
  for (const o of live) pool.release(o);
  assert.equal(pool.outstanding, 0);
  assert.equal(pool.totalAcquires, pool.totalReleases);
});

test("releaseAll recovers every checked-out object (teardown escape hatch)", () => {
  const { pool } = makePool();
  pool.acquire();
  pool.acquire();
  pool.releaseAll();
  assert.equal(pool.outstanding, 0);
  assert.throws(() => pool.releaseAll && pool.release({ id: -1, dirty: 0 }), /unknown/);
});

test("drain empties the freelist; checked-out objects are unaffected", () => {
  const { pool } = makePool();
  const o = pool.acquire();
  pool.drain();
  assert.equal(pool.idleCount, 0);
  pool.release(o);
  assert.equal(pool.idleCount, 1);
});
