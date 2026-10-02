import { test } from "node:test";
import assert from "node:assert/strict";
import { SlidingWindowDurationIntegrator } from "../src/SlidingWindowDurationIntegrator.ts";

const WINDOW_MS = 100_000; // 100 s sliding window
const isOverTemp = (t: number): boolean => t > 80;

/** Builds the acceptance scenario: 75C baseline, 85C at t=10s, 75C at t=40s. */
function makeAcceptanceIntegrator(): SlidingWindowDurationIntegrator {
  const integ = new SlidingWindowDurationIntegrator(WINDOW_MS, isOverTemp);
  integ.addSample(0, 75);
  integ.addSample(10_000, 85); // alarm on at t=10s
  integ.addSample(40_000, 75); // alarm off at t=40s
  return integ;
}

test("acceptance: 30s of over-temp accumulated at t=50s", () => {
  const integ = makeAcceptanceIntegrator();
  assert.equal(integ.totalActiveMs(50_000), 30_000);
});

test("acceptance: contribution deducted exactly as t=10s slides out (t=120s -> 20s)", () => {
  const integ = makeAcceptanceIntegrator();
  // Window is [20s, 120s]; alarm interval [10s, 40s] overlaps it by 20s.
  assert.equal(integ.totalActiveMs(120_000), 20_000);
});

test("acceptance: over-temp time smoothly decreases to zero as window slides", () => {
  const integ = makeAcceptanceIntegrator();
  assert.equal(integ.totalActiveMs(110_000), 30_000); // full alarm still inside
  assert.equal(integ.totalActiveMs(111_000), 29_000); // 1s slid out -> 1s deducted
  assert.equal(integ.totalActiveMs(120_000), 20_000);
  assert.equal(integ.totalActiveMs(139_000), 1_000);
  assert.equal(integ.totalActiveMs(140_000), 0); // fully slid out
  assert.equal(integ.totalActiveMs(200_000), 0); // stays zero, no residue
});

test("no drift: dense sampling of the same step function yields identical results", () => {
  // A naive discrete integrator accumulates rounding error per sample; the
  // transition-based integrator must return the exact same values whether the
  // stream is sampled coarsely or densely.
  const dense = new SlidingWindowDurationIntegrator(WINDOW_MS, isOverTemp);
  for (let t = 0; t <= 200_000; t += 100) {
    dense.addSample(t, t >= 10_000 && t < 40_000 ? 85 : 75);
  }
  assert.equal(dense.totalActiveMs(50_000), 30_000);
  assert.equal(dense.totalActiveMs(120_000), 20_000);
  assert.equal(dense.totalActiveMs(140_000), 0);
});

test("no drift: repeated queries are idempotent", () => {
  const integ = makeAcceptanceIntegrator();
  for (let i = 0; i < 1000; i++) {
    assert.equal(integ.totalActiveMs(120_000), 20_000);
  }
});

test("ongoing alarm counts up to the query instant", () => {
  const integ = new SlidingWindowDurationIntegrator(WINDOW_MS, isOverTemp);
  integ.addSample(0, 90); // alarm on from the very first sample
  assert.equal(integ.totalActiveMs(30_000), 30_000);
  assert.equal(integ.totalActiveMs(150_000), 100_000); // saturated at window size
});

test("multiple alarm episodes within the window are summed", () => {
  const integ = new SlidingWindowDurationIntegrator(WINDOW_MS, isOverTemp);
  integ.addSample(0, 75);
  integ.addSample(5_000, 90);
  integ.addSample(15_000, 70); // episode 1: 10s
  integ.addSample(60_000, 81);
  integ.addSample(85_000, 79); // episode 2: 25s
  assert.equal(integ.totalActiveMs(90_000), 35_000);
});

test("condition boundary is strict: exactly 80C is not an alarm", () => {
  const integ = new SlidingWindowDurationIntegrator(WINDOW_MS, isOverTemp);
  integ.addSample(0, 80);
  assert.equal(integ.totalActiveMs(50_000), 0);
  integ.addSample(50_000, 80.0001);
  assert.equal(integ.totalActiveMs(60_000), 10_000);
});

test("out-of-order samples are rejected", () => {
  const integ = new SlidingWindowDurationIntegrator(WINDOW_MS, isOverTemp);
  integ.addSample(10_000, 85);
  assert.throws(() => integ.addSample(9_999, 75), RangeError);
});

test("invalid window size is rejected", () => {
  assert.throws(() => new SlidingWindowDurationIntegrator(0, isOverTemp), RangeError);
  assert.throws(() => new SlidingWindowDurationIntegrator(-1, isOverTemp), RangeError);
});
