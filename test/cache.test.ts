import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { GenerationCache } from "../src/cache.ts";

/** Budget is charged by the actual entry size, not by entry count. */
function makeCache(budget: number) {
  return new GenerationCache<string>(budget, (value) => value.length * 2 + 8);
}

describe("GenerationCache", () => {
  it("accounts budget by actual entry size", () => {
    const cache = makeCache(40);
    assert.equal(cache.set("a", "1234", 0), true); // 16 bytes
    assert.equal(cache.set("b", "12", 0), true); // 12 bytes -> 28 total
    assert.equal(cache.usedBytes, 28);
    assert.equal(cache.set("c", "123456", 0), true); // 20 bytes -> evicts "a"
    assert.equal(cache.peek("a"), undefined);
    assert.equal(cache.usedBytes, 32);
    assert.ok(cache.usedBytes <= cache.budgetBytes);
  });

  it("keeps byte accounting conserved across inserts and evictions", () => {
    const cache = makeCache(64);
    for (let i = 0; i < 50; i += 1) {
      cache.set(`k${i}`, "x".repeat(i % 7), 0);
      assert.ok(cache.checkConservation(), `conservation broken at i=${i}`);
      assert.ok(cache.usedBytes <= cache.budgetBytes);
    }
  });

  it("never evicts borrowed entries, even over budget", () => {
    const cache = makeCache(24);
    cache.set("pinned", "1234", 0); // 16 bytes
    const handle = cache.borrow("pinned");
    assert.ok(handle);
    cache.set("other", "12345678", 0); // 24 bytes, total 40 > budget
    assert.equal(cache.peek("pinned"), "1234", "borrowed entry survives eviction");
    assert.equal(cache.peek("other"), "12345678");
    assert.ok(cache.checkConservation(), "accounting stays exact while over budget");
    handle!.release();
    assert.ok(cache.usedBytes <= cache.budgetBytes, "eviction resumes after release");
    assert.equal(cache.peek("pinned"), undefined);
  });

  it("swaps generations atomically and rejects stale-generation writes", () => {
    const cache = makeCache(1024);
    cache.set("a", "old", 0);
    cache.set("b", "old", 0);
    const staleGeneration = cache.currentGeneration;
    const next = cache.swapGeneration();
    assert.equal(cache.size, 0, "old generation disappears in one step");
    assert.equal(cache.usedBytes, 0);
    assert.equal(cache.set("a", "stale-writer", staleGeneration), false);
    assert.equal(cache.peek("a"), undefined, "stale async writer cannot repopulate");
    assert.equal(cache.set("a", "fresh", next), true);
    assert.equal(cache.peek("a"), "fresh");
  });

  it("drops borrows of the old generation on swap", () => {
    const cache = makeCache(1024);
    cache.set("a", "old", 0);
    const handle = cache.borrow("a");
    cache.swapGeneration();
    assert.equal(cache.borrow("a"), undefined);
    handle!.release(); // releasing a dead handle is harmless
    assert.ok(cache.checkConservation());
  });

  it("evicts least-recently-used first", () => {
    const cache = makeCache(32);
    cache.set("a", "1234", 0); // 16
    cache.set("b", "1234", 0); // 16
    cache.get("a"); // touch: b is now the oldest
    cache.set("c", "1234", 0); // evicts b
    assert.equal(cache.peek("b"), undefined);
    assert.equal(cache.peek("a"), "1234");
    assert.equal(cache.peek("c"), "1234");
  });
});
