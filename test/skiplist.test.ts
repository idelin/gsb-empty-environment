import { test } from "node:test";
import assert from "node:assert/strict";
import { SkipList, type Entry } from "../src/skiplist.ts";

/** Deterministic PRNG (mulberry32) so test runs are reproducible. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function assertStrictlyIncreasing(entries: Entry<number, unknown>[]): void {
  for (let i = 1; i < entries.length; i++) {
    assert.ok(
      entries[i - 1]!.key < entries[i]!.key,
      `keys must be strictly increasing: ` +
        `entries[${i - 1}].key=${entries[i - 1]!.key} !< entries[${i}].key=${entries[i]!.key}`,
    );
  }
}

const yieldToEventLoop = () => new Promise<void>((r) => setImmediate(r));

test("10,000 random inserts: full scan is strictly increasing and complete", () => {
  const rng = mulberry32(42);
  const list = new SkipList<number, number>();
  const expected = new Map<number, number>();

  for (let i = 0; i < 10_000; i++) {
    const key = Math.floor(rng() * 50_000); // collisions exercise overwrite path
    const value = i;
    list.insert(key, value);
    expected.set(key, value);
  }

  assert.equal(list.size, expected.size, "unique key count must match");

  const entries = list.entries();
  assert.equal(entries.length, expected.size, "scan must return every key");
  assertStrictlyIncreasing(entries);

  // Values must reflect the latest write per key.
  for (const { key, value } of entries) {
    assert.equal(value, expected.get(key), `stale value for key ${key}`);
  }

  // Spot-check point lookups against the reference map.
  for (let i = 0; i < 1_000; i++) {
    const key = Math.floor(rng() * 50_000);
    assert.equal(list.get(key), expected.get(key), `get(${key}) mismatch`);
  }
});

test("range iterator: arbitrary interval is bounded and strictly increasing", () => {
  const rng = mulberry32(7);
  const list = new SkipList<number, string>();

  for (let i = 0; i < 10_000; i++) {
    const key = Math.floor(rng() * 100_000);
    list.insert(key, `v${key}`);
  }

  // Probe many random intervals, including empty and inverted ones.
  for (let probe = 0; probe < 100; probe++) {
    const a = Math.floor(rng() * 100_000);
    const b = Math.floor(rng() * 100_000);
    const lo = Math.min(a, b);
    const hi = rng() < 0.1 ? lo - 1 : Math.max(a, b); // sometimes empty range

    const entries = list.entries({ gte: lo, lte: hi });
    assertStrictlyIncreasing(entries);
    for (const { key } of entries) {
      assert.ok(key >= lo, `key ${key} below lower bound ${lo}`);
      assert.ok(key <= hi, `key ${key} above upper bound ${hi}`);
    }

    // Cross-check against a brute-force filter of the full scan.
    const expected = list
      .entries()
      .filter((e) => e.key >= lo && e.key <= hi);
    assert.deepEqual(entries, expected, `range [${lo}, ${hi}] mismatch`);
  }
});

test("snapshot isolation: iterator is stable while inserts race the scan", async () => {
  const list = new SkipList<number, number>();

  for (let i = 0; i < 5_000; i++) {
    list.insert(i * 2, i); // even keys 0..9998
  }

  // Freeze the snapshot BEFORE any concurrent writer starts.
  const iterator = list.iterator({ gte: 1_000, lte: 8_999 });
  const expected = list.entries({ gte: 1_000, lte: 8_999 });

  // Concurrent writers: insert brand-new keys (odd keys, plus keys inside
  // the scanned range) and overwrite existing keys, yielding often so the
  // writes interleave with the iteration below.
  const writers = Array.from({ length: 4 }, async (_, w) => {
    for (let i = 0; i < 2_500; i++) {
      const key = (i * 4 + w) % 10_000;
      list.insert(key, -1 - w); // overwrites + brand-new odd keys
      if (i % 7 === 0) await yieldToEventLoop();
    }
  });

  // Drain the iterator slowly, yielding to let writers run mid-scan.
  const observed: Entry<number, number>[] = [];
  let steps = 0;
  while (true) {
    const { done, value } = iterator.next();
    if (done) break;
    observed.push(value);
    if (++steps % 3 === 0) await yieldToEventLoop();
  }
  await Promise.all(writers);

  // The iterator's result set must be identical to the pre-write snapshot.
  assert.deepEqual(
    observed,
    expected,
    "iterator output must be fully isolated from concurrent inserts",
  );
  assertStrictlyIncreasing(observed);

  // Sanity: the table itself did absorb the concurrent writes.
  assert.equal(list.get(0), -1, "overwrite must be visible to new readers");
  assert.ok(list.size > 5_000, "new keys must be visible to new readers");

  // A fresh iterator sees the new state; the old snapshot does not.
  const fresh = list.entries({ gte: 1_000, lte: 8_999 });
  assert.notDeepEqual(fresh, expected);
});

test("explicit snapshotSeq: historical snapshots remain readable", () => {
  const list = new SkipList<number, string>();
  const snapshots: number[] = [];

  for (let round = 0; round < 10; round++) {
    for (let i = 0; i < 100; i++) {
      list.insert(round * 100 + i, `r${round}`);
    }
    snapshots.push(list.currentSeq);
  }

  for (let round = 0; round < 10; round++) {
    const entries = list.entries({ snapshotSeq: snapshots[round]! });
    assert.equal(entries.length, (round + 1) * 100);
    assertStrictlyIncreasing(entries);
    for (const { key, value } of entries) {
      assert.equal(value, `r${Math.floor(key / 100)}`);
    }
  }
});

test("overwrite under snapshot: old iterator keeps old value", () => {
  const list = new SkipList<number, string>();
  list.insert(1, "one");
  list.insert(2, "two");

  const before = list.iterator();
  list.insert(1, "ONE");
  list.insert(3, "three");

  assert.deepEqual([...before], [
    { key: 1, value: "one" },
    { key: 2, value: "two" },
  ]);
  assert.deepEqual([...list.iterator()], [
    { key: 1, value: "ONE" },
    { key: 2, value: "two" },
    { key: 3, value: "three" },
  ]);
});
