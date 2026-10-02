import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ZeroAllocCache } from "../src/cache.ts";

describe("ZeroAllocCache", () => {
  it("stores and retrieves values", () => {
    const c = new ZeroAllocCache<string>(16);
    assert.equal(c.get(1), undefined);
    c.getOrLoad(1, (k) => `v${k}`);
    assert.equal(c.get(1), "v1");
    assert.equal(c.size, 1);
  });

  it("hit path never invokes the loader", () => {
    const c = new ZeroAllocCache<number>(8);
    let loads = 0;
    const loader = (k: number) => {
      loads++;
      return k * 2;
    };
    assert.equal(c.getOrLoad(5, loader), 10);
    assert.equal(loads, 1);
    for (let i = 0; i < 1000; i++) {
      assert.equal(c.getOrLoad(5, loader), 10);
    }
    assert.equal(loads, 1, "loader ran only on the initial miss");
    const s = c.getOrLoad(5, loader);
    assert.equal(s, 10);
    assert.equal(c.stats().hits, 1001);
    assert.equal(c.stats().misses, 1);
  });

  it("respects capacity and evicts deterministically when full", () => {
    const c = new ZeroAllocCache<number>(4);
    for (let k = 0; k < 4; k++) c.getOrLoad(k, (x) => x);
    assert.equal(c.size, 4);
    // Fill beyond capacity: forces eviction path, size stays bounded.
    for (let k = 4; k < 100; k++) c.getOrLoad(k, (x) => x);
    assert.ok(c.size <= 4, `size bounded by capacity, got ${c.size}`);
    assert.ok(c.stats().evictions > 0);
  });

  it("handles boundary keys: 0, negatives, large integers", () => {
    const c = new ZeroAllocCache<string>(32);
    const keys = [0, -1, -(2 ** 31), 2 ** 31, Number.MAX_SAFE_INTEGER, -0];
    for (const k of keys) c.getOrLoad(k, (x) => `k${x}`);
    for (const k of keys) assert.equal(c.get(k), `k${k}`, `key ${k}`);
  });

  it("clear() empties the cache and allows reuse", () => {
    const c = new ZeroAllocCache<number>(8);
    for (let k = 0; k < 8; k++) c.getOrLoad(k, (x) => x);
    c.clear();
    assert.equal(c.size, 0);
    assert.equal(c.get(3), undefined);
    c.getOrLoad(3, () => 33);
    assert.equal(c.get(3), 33);
  });

  it("rejects invalid capacity", () => {
    assert.throws(() => new ZeroAllocCache(0), RangeError);
    assert.throws(() => new ZeroAllocCache(-5), RangeError);
    assert.throws(() => new ZeroAllocCache(2.5), RangeError);
  });

  it("capacity 1 edge case: every distinct key evicts", () => {
    const c = new ZeroAllocCache<number>(1);
    assert.equal(c.getOrLoad(1, (k) => k), 1);
    assert.equal(c.getOrLoad(2, (k) => k), 2);
    assert.equal(c.get(1), undefined, "previous entry evicted");
    assert.equal(c.get(2), 2);
    assert.equal(c.size, 1);
  });

  it("internal storage never grows after construction (zero realloc)", () => {
    const c = new ZeroAllocCache<number>(64);
    for (let k = 0; k < 10_000; k++) c.getOrLoad(k, (x) => x);
    const s = c.stats();
    assert.equal(s.capacity, 64);
    assert.ok(s.size <= 64);
  });

  it("is safe under async interleaving (single-threaded atomicity)", async () => {
    const c = new ZeroAllocCache<number>(128);
    let loads = 0;
    const tasks: Promise<number>[] = [];
    for (let i = 0; i < 5000; i++) {
      const key = i % 50; // heavy key reuse across "concurrent" tasks
      tasks.push((async () => {
        await Promise.resolve();
        return c.getOrLoad(key, (k) => {
          loads++;
          return k * 10;
        });
      })());
    }
    const results = await Promise.all(tasks);
    for (let i = 0; i < results.length; i++) {
      assert.equal(results[i], (i % 50) * 10);
    }
    assert.ok(loads <= 50, `each key computed at most once, loads=${loads}`);
    assert.ok(c.size <= 50);
  });
});
