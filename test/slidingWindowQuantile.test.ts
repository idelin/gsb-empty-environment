import { test } from "node:test";
import assert from "node:assert/strict";
import { SlidingWindowQuantile } from "../src/slidingWindowQuantile.ts";

const WINDOW_MS = 10_000;

function makeWindow(): SlidingWindowQuantile {
  return new SlidingWindowQuantile(WINDOW_MS);
}

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

test("acceptance: q=0.5 over [1, 1, 9, 20] is 1", () => {
  const w = makeWindow();
  w.add({ id: "a", timestamp: 1000, value: 1 });
  w.add({ id: "b", timestamp: 1000, value: 1 });
  w.add({ id: "c", timestamp: 1000, value: 9 });
  w.add({ id: "d", timestamp: 1000, value: 20 });
  // floor(0.5 * (4 - 1)) = 1 -> sorted[1] = 1
  assert.equal(w.quantile(0.5), 1);
});

test("acceptance: retracting one of the equal 1s yields 9", () => {
  const w = makeWindow();
  w.add({ id: "a", timestamp: 1000, value: 1 });
  w.add({ id: "b", timestamp: 1000, value: 1 });
  w.add({ id: "c", timestamp: 1000, value: 9 });
  w.add({ id: "d", timestamp: 1000, value: 20 });
  assert.equal(w.retract("a"), true);
  // remaining [1, 9, 20]; floor(0.5 * 2) = 1 -> 9
  assert.equal(w.quantile(0.5), 9);
  // the other 1 is still live: equal values correctly deduplicated by id
  assert.deepEqual(w.values(), [1, 9, 20]);
  assert.equal(w.retract("a"), false);
});

test("floor(q*(n-1)) rank positioning", () => {
  const w = makeWindow();
  [10, 20, 30, 40, 50].forEach((v, i) => w.add({ id: `e${i}`, timestamp: 500, value: v }));
  assert.equal(w.quantile(0), 10); // k = 0
  assert.equal(w.quantile(0.25), 20); // k = floor(1) = 1
  assert.equal(w.quantile(0.5), 30); // k = 2
  assert.equal(w.quantile(0.75), 40); // k = 3
  assert.equal(w.quantile(1), 50); // k = 4
  assert.equal(w.quantile(0.4999), 20); // k = floor(1.9996) = 1
});

test("events expire as the 10s window slides", () => {
  const w = makeWindow();
  w.add({ id: "old", timestamp: 0, value: 100 });
  w.add({ id: "mid", timestamp: 5_000, value: 50 });
  assert.equal(w.size, 2);
  w.add({ id: "new", timestamp: 10_000, value: 1 });
  // window is (0, 10000]: the t=0 event expired exactly at the boundary
  assert.deepEqual(w.values(), [1, 50]);
  w.advance(15_001);
  // window is (5001, 15001]: the t=5000 event expired too
  assert.deepEqual(w.values(), [1]);
  w.advance(20_001);
  assert.equal(w.size, 0);
  assert.equal(w.quantile(0.5), undefined);
});

test("boundary: event at exactly now - windowMs expires, one ms later survives", () => {
  const w = makeWindow();
  w.add({ id: "edge", timestamp: 1_000, value: 7 });
  w.add({ id: "safe", timestamp: 1_001, value: 8 });
  w.advance(11_000);
  assert.deepEqual(w.values(), [8]);
});

test("out-of-order insertion stays exact", () => {
  const w = makeWindow();
  w.add({ id: "e1", timestamp: 9_000, value: 90 });
  w.add({ id: "e2", timestamp: 2_000, value: 20 });
  w.add({ id: "e3", timestamp: 7_500, value: 75 });
  w.add({ id: "e4", timestamp: 1_500, value: 15 });
  assert.deepEqual(w.values(), [15, 20, 75, 90]);
  assert.equal(w.quantile(0.5), 20);
  w.add({ id: "e5", timestamp: 20_000, value: 5 });
  // a late event that is already expired is rejected
  assert.throws(() => w.add({ id: "late", timestamp: 9_999, value: 1 }), /expired/);
});

test("single-event retraction among duplicates", () => {
  const w = makeWindow();
  for (let i = 0; i < 5; i++) w.add({ id: `d${i}`, timestamp: 100, value: 3 });
  w.retract("d2");
  assert.deepEqual(w.values(), [3, 3, 3, 3]);
  assert.equal(w.quantile(0.5), 3);
  w.retract("d0");
  w.retract("d1");
  w.retract("d3");
  w.retract("d4");
  assert.equal(w.size, 0);
});

test("continuous inflow with expiry matches naive model", () => {
  const rand = mulberry32(7);
  const w = makeWindow();
  const live = new Map<string, { timestamp: number; value: number }>();
  let now = 0;
  for (let step = 0; step < 2000; step++) {
    now += Math.floor(rand() * 200);
    const roll = rand();
    if (roll < 0.7) {
      const id = `s${step}`;
      const event = { timestamp: now, value: Math.floor(rand() * 100) };
      w.add({ id, ...event });
      live.set(id, event);
    } else if (roll < 0.8 && live.size > 0) {
      const ids = [...live.keys()];
      const id = ids[Math.floor(rand() * ids.length)];
      assert.equal(w.retract(id), true);
      live.delete(id);
    }
    w.advance(now);
    for (const [id, e] of live) {
      if (e.timestamp <= now - WINDOW_MS) live.delete(id);
    }
    const sorted = [...live.values()].map((e) => e.value).sort((a, b) => a - b);
    assert.equal(w.size, sorted.length);
    if (sorted.length > 0) {
      const q = rand();
      const expected = sorted[Math.floor(q * (sorted.length - 1))];
      assert.equal(w.quantile(q), expected);
    } else {
      assert.equal(w.quantile(0.5), undefined);
    }
  }
});

test("invalid arguments are rejected", () => {
  const w = makeWindow();
  assert.throws(() => w.quantile(-0.1), /RangeError|\[0, 1\]/);
  assert.throws(() => w.quantile(1.1), /RangeError|\[0, 1\]/);
  w.add({ id: "x", timestamp: 1, value: 1 });
  assert.throws(() => w.add({ id: "x", timestamp: 2, value: 2 }), /duplicate/);
  assert.throws(() => new SlidingWindowQuantile(0), /RangeError|positive/);
});
