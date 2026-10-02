import { test } from "node:test";
import assert from "node:assert/strict";
import { Counter, Gauge, Histogram, RateLimiterMetrics } from "../src/metrics.ts";
import { FakeClock } from "./helpers.ts";

test("counter and gauge basics", () => {
  const c = new Counter();
  assert.equal(c.value, 0);
  c.inc();
  c.inc(41);
  assert.equal(c.value, 42);

  const g = new Gauge();
  g.set(3.5);
  assert.equal(g.value, 3.5);
});

test("histogram cumulative buckets, count and sum", () => {
  const h = new Histogram([0.1, 0.5, 1]);
  h.observe(0.05); // hits all 3 buckets
  h.observe(0.4);  // hits 0.5 and 1
  h.observe(0.9);  // hits 1
  h.observe(7);    // hits none (only +Inf)
  const snap = h.snapshot();
  assert.deepEqual(
    snap.buckets.map((b) => [b.le, b.count]),
    [[0.1, 1], [0.5, 2], [1, 3]],
  );
  assert.equal(snap.count, 4);
  assert.ok(Math.abs(snap.sum - (0.05 + 0.4 + 0.9 + 7)) < 1e-9);
});

test("histogram rejects empty or duplicate bounds", () => {
  assert.throws(() => new Histogram([]));
  assert.throws(() => new Histogram([1, 1]));
});

test("window snapshot rotates and conserves totals", () => {
  const clock = new FakeClock();
  const m = new RateLimiterMetrics(clock);

  m.recordAllowed(9);
  m.recordAllowed(8);
  m.recordDropped(0);
  clock.advance(1000);

  const s1 = m.snapshot();
  assert.equal(s1.window.allowed, 2);
  assert.equal(s1.window.dropped, 1);
  assert.equal(s1.allowedTotal, 2);
  assert.equal(s1.droppedTotal, 1);
  assert.equal(s1.windowThroughputPerSec, 2); // 2 allowed / 1s window
  assert.equal(s1.tokenLevel, 0);

  // Next window starts empty; totals keep accumulating.
  m.recordAllowed(7);
  clock.advance(500);
  const s2 = m.snapshot();
  assert.equal(s2.window.allowed, 1);
  assert.equal(s2.window.dropped, 0);
  assert.equal(s2.allowedTotal, 3);
  assert.equal(s2.droppedTotal, 1);

  // Conservation across windows: sum of windows == totals.
  assert.equal(s1.window.allowed + s2.window.allowed, s2.allowedTotal);
  assert.equal(s1.window.dropped + s2.window.dropped, s2.droppedTotal);
});

test("recording is sub-microsecond on average", () => {
  const m = new RateLimiterMetrics();
  const iterations = 200_000;
  // Warm up JIT.
  for (let i = 0; i < 10_000; i++) {
    m.recordAllowed(1);
    m.recordDropped(0);
    m.recordWaitDuration(0.001);
  }
  const start = process.hrtime.bigint();
  for (let i = 0; i < iterations; i++) {
    m.recordAllowed(1);
    m.recordDropped(0);
    m.recordWaitDuration(0.001);
  }
  const elapsedNs = Number(process.hrtime.bigint() - start);
  const avgNsPerOp = elapsedNs / (iterations * 3);
  assert.ok(
    avgNsPerOp < 1000,
    `avg record op took ${avgNsPerOp.toFixed(1)}ns, expected < 1000ns`,
  );
});
