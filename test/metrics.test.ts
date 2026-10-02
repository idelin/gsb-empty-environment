import { test } from "node:test";
import assert from "node:assert/strict";
import { RateLimiterMetrics } from "../src/metrics.ts";

/** Deterministic clock driven by the test. */
function fakeClock(startMs = 0) {
  let now = startMs;
  return {
    now: () => now,
    advance: (ms: number) => { now += ms; },
  };
}

test("per-window allowed/dropped counts are exact and conserve totals", () => {
  const clock = fakeClock(1_000);
  const metrics = new RateLimiterMetrics({
    windowMs: 100,
    windowCount: 10,
    nowMs: clock.now,
  });
  // Window @1000: 3 allowed, 2 dropped.
  for (let i = 0; i < 3; i++) metrics.recordAllowed();
  for (let i = 0; i < 2; i++) metrics.recordDropped();
  clock.advance(100);
  // Window @1100: 1 allowed, 4 dropped.
  metrics.recordAllowed();
  for (let i = 0; i < 4; i++) metrics.recordDropped();
  clock.advance(250); // skip one idle window, land mid-window @1350
  metrics.recordDropped();

  const snap = metrics.snapshot(7);
  assert.equal(snap.allowedTotal, 4);
  assert.equal(snap.droppedTotal, 7);
  assert.equal(snap.requestsTotal, 11);

  const windows = snap.windows;
  const w0 = windows.find((w) => w.startMs === 1000)!;
  const w1 = windows.find((w) => w.startMs === 1100)!;
  const wIdle = windows.find((w) => w.startMs === 1200)!;
  const wCur = windows[windows.length - 1];
  assert.deepEqual([w0.allowed, w0.dropped], [3, 2]);
  assert.deepEqual([w1.allowed, w1.dropped], [1, 4]);
  assert.deepEqual([wIdle.allowed, wIdle.dropped], [0, 0]);
  assert.equal(wCur.current, true);
  assert.deepEqual([wCur.allowed, wCur.dropped], [0, 1]);

  // Conservation per window and overall.
  const sumAllowed = windows.reduce((a, w) => a + w.allowed, 0);
  const sumDropped = windows.reduce((a, w) => a + w.dropped, 0);
  assert.equal(sumAllowed, snap.allowedTotal);
  assert.equal(sumDropped, snap.droppedTotal);
});

test("throughput and droppedLastWindow come from the last completed window", () => {
  const clock = fakeClock(0);
  const metrics = new RateLimiterMetrics({
    windowMs: 1000,
    nowMs: clock.now,
  });
  for (let i = 0; i < 250; i++) metrics.recordAllowed();
  for (let i = 0; i < 50; i++) metrics.recordDropped();
  clock.advance(1000); // close window @0
  const snap = metrics.snapshot(0);
  assert.equal(snap.throughputPerSec, 250);
  assert.equal(snap.droppedLastWindow, 50);
});

test("window ring retains only the configured number of windows", () => {
  const clock = fakeClock(0);
  const metrics = new RateLimiterMetrics({
    windowMs: 10,
    windowCount: 5,
    nowMs: clock.now,
  });
  metrics.recordAllowed();
  clock.advance(1_000);
  metrics.recordAllowed();
  const snap = metrics.snapshot(0);
  assert.equal(snap.windows.length, 5);
  assert.equal(snap.windows[snap.windows.length - 1].startMs, 1000);
});

test("wait-duration histogram: cumulative buckets, sum, min, max", () => {
  const metrics = new RateLimiterMetrics({
    bucketBoundsSec: [0.001, 0.01, 0.1],
  });
  metrics.recordWait(500);        // 0.5us  -> bucket 1ms
  metrics.recordWait(5_000);      // 5us    -> bucket 10ms
  metrics.recordWait(50_000);     // 50us   -> bucket 100ms
  metrics.recordWait(500_000_000); // 0.5s  -> +Inf only
  const hist = metrics.snapshot(0).waitDuration;
  assert.equal(hist.count, 4);
  assert.equal(hist.sumNanos, 500_055_500);
  assert.equal(hist.minNanos, 500);
  assert.equal(hist.maxNanos, 500_000_000);
  assert.deepEqual(
    hist.buckets.map((b) => b.count),
    [1, 2, 3],
    "buckets must be cumulative",
  );
  assert.deepEqual(
    hist.buckets.map((b) => b.le),
    [0.001, 0.01, 0.1],
  );
});

test("metrics recording overhead is sub-microsecond per sample", () => {
  const metrics = new RateLimiterMetrics({});
  // Warm up JIT.
  for (let i = 0; i < 50_000; i++) {
    metrics.recordAllowed();
    metrics.recordDropped();
  }
  // Best-of-several rounds keeps the check stable on contended CI machines.
  const ROUNDS = 5;
  const N = 200_000;
  let bestNsPerSample = Number.POSITIVE_INFINITY;
  for (let round = 0; round < ROUNDS; round++) {
    const t0 = performance.now();
    for (let i = 0; i < N; i++) {
      metrics.recordAllowed();
      metrics.recordDropped();
    }
    const nsPerSample = ((performance.now() - t0) * 1e6) / (2 * N);
    bestNsPerSample = Math.min(bestNsPerSample, nsPerSample);
  }
  const snap = metrics.snapshot(0);
  assert.equal(snap.allowedTotal, 50_000 + ROUNDS * N);
  assert.equal(snap.droppedTotal, 50_000 + ROUNDS * N);
  console.log(
    `    metrics hot path: best ${bestNsPerSample.toFixed(1)} ns/sample ` +
      `over ${ROUNDS} rounds x ${(2 * N).toLocaleString()} samples`,
  );
  assert.ok(
    bestNsPerSample < 1000,
    `expected sub-microsecond recording, got ${bestNsPerSample.toFixed(1)} ns/sample`,
  );
});
