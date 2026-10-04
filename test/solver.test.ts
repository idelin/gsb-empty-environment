import { test } from "node:test";
import assert from "node:assert/strict";
import { Fraction } from "../src/fraction.ts";
import { fitSegmentedRegression } from "../src/segmentedRegression.ts";

test("Fraction: normalization, arithmetic, exact comparison", () => {
  assert.equal(new Fraction(2n, 4n).toString(), "1/2");
  assert.equal(new Fraction(3n, -6n).toString(), "-1/2");
  assert.equal(new Fraction(-3n, -6n).toString(), "1/2");
  assert.equal(new Fraction(10n, 5n).toString(), "2");
  assert.equal(new Fraction(1n, 3n).add(new Fraction(1n, 6n)).toString(), "1/2");
  assert.equal(new Fraction(1n, 3n).mul(new Fraction(3n, 4n)).toString(), "1/4");
  assert.equal(new Fraction(1n, 2n).sub(new Fraction(2n, 3n)).toString(), "-1/6");
  assert.equal(new Fraction(1n, 2n).div(new Fraction(3n, 4n)).toString(), "2/3");
  // Exact comparison of values that are indistinguishable in float64.
  const a = new Fraction(1n, 10n ** 30n);
  const b = new Fraction(1n, 10n ** 30n + 1n);
  assert.equal(a.cmp(b), 1);
  assert.equal(Number(1 / 1e30) === Number(1 / (1e30 + 1)), true); // floats tie
  assert.throws(() => new Fraction(1n, 0n), /zero/);
  assert.throws(() => Fraction.from(1.5), /non-integer/);
});

test("recovers exact breakpoints of two perfect lines", () => {
  // y = 2x + 1 for x in [0..4], y = -x + 20 for x in [5..9]
  const xs = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9];
  const ys = [1, 3, 5, 7, 9, 15, 14, 13, 12, 11];
  const r = fitSegmentedRegression(xs, ys, 3);
  assert.equal(r.segments.length, 2);
  assert.deepEqual(r.breakpoints, [5]);
  assert.equal(r.totalSSE.toString(), "0");
  assert.equal(r.segments[0].slope.toString(), "2");
  assert.equal(r.segments[0].intercept.toString(), "1");
  assert.equal(r.segments[1].slope.toString(), "-1");
  assert.equal(r.segments[1].intercept.toString(), "20");
  assert.equal(r.segments.every((s) => !s.degenerate), true);
});

test("equal error prefers fewer segments", () => {
  // Perfectly collinear: any extra segment keeps SSE at exactly 0.
  const xs = [0, 1, 2, 3, 4, 5, 6, 7, 8];
  const ys = xs.map((x) => 3 * x - 2);
  const r = fitSegmentedRegression(xs, ys, 3);
  assert.equal(r.segments.length, 1);
  assert.deepEqual(r.breakpoints, []);
  assert.equal(r.totalSSE.toString(), "0");
  assert.equal(r.segments[0].slope.toString(), "3");
  assert.equal(r.segments[0].intercept.toString(), "-2");
});

test("exact rational parameters and SSE on non-trivial data", () => {
  // Single segment over x = 0,1,2 with y = 0,0,1.
  const r = fitSegmentedRegression([0, 1, 2], [0, 0, 1], 1);
  assert.equal(r.segments[0].slope.toString(), "1/2");
  assert.equal(r.segments[0].intercept.toString(), "-1/6");
  assert.equal(r.segments[0].sse.toString(), "1/6");
  assert.equal(r.totalSSE.toString(), "1/6");
});

test("degenerate segment (all x identical) is handled explicitly", () => {
  // First three points share x = 5; the rest lie on y = x - 3.
  const xs = [5, 5, 5, 3, 4, 5];
  const ys = [1, 2, 4, 0, 1, 2];
  const r = fitSegmentedRegression(xs, ys, 2);
  assert.equal(r.segments.length, 2);
  const deg = r.segments[0];
  assert.equal(deg.degenerate, true);
  assert.equal(deg.slope.toString(), "0");
  assert.equal(deg.intercept.toString(), "7/3"); // mean of 1, 2, 4
  assert.equal(deg.sse.toString(), "14/3"); // sum of squared deviations
  const line = r.segments[1];
  assert.equal(line.degenerate, false);
  assert.equal(line.slope.toString(), "1");
  assert.equal(line.intercept.toString(), "-3");
  assert.equal(r.totalSSE.toString(), "14/3");
});

test("duplicate x values without full degeneracy still fit a slope", () => {
  // x has duplicates but varies within the segment: ordinary least squares.
  const xs = [1, 1, 2, 3, 3];
  const ys = [2, 4, 5, 8, 6];
  const r = fitSegmentedRegression(xs, ys, 1);
  assert.equal(r.segments[0].degenerate, false);
  // Verified against the exact normal equations:
  // sum x = 10, sum y = 25, sum xx = 24, sum xy = 58
  // slope = (5*58 - 10*25) / (5*24 - 100) = 40/20 = 2
  assert.equal(r.segments[0].slope.toString(), "2");
  // intercept = (25*20 - 40*10) / (5*20) = 100/100 = 1
  assert.equal(r.segments[0].intercept.toString(), "1");
});

test("huge coordinates stay exact where float64 would lose precision", () => {
  const B = 10n ** 18n;
  const xs = [0, 1, 2];
  const ys = [B, B, B + 1n]; // same shape as [0,0,1] shifted by 1e18
  const r = fitSegmentedRegression(xs, ys, 1);
  assert.equal(r.segments[0].slope.toString(), "1/2");
  assert.equal(r.segments[0].sse.toString(), "1/6");
  // intercept = mean(y) - slope*mean(x) = (3B+1)/3 - 1/2 = (6B-1)/6
  assert.equal(r.segments[0].intercept.toString(), `${6n * B - 1n}/6`);
});

test("optimal split chosen by exact comparison, not float rounding", () => {
  // Two exact lines with one outlier; huge baseline makes float64 SSEs
  // of competing splits coincide, but the exact answer is unambiguous.
  const B = 10n ** 18n;
  const xs = [0, 1, 2, 3, 4, 5];
  const ys = [B, B + 1n, B + 2n, B + 10n, B + 9n, B + 8n];
  // True structure: slope +1 then slope -1, breakpoint at 3, SSE 0.
  const r = fitSegmentedRegression(xs, ys, 2);
  assert.deepEqual(r.breakpoints, [3]);
  assert.equal(r.totalSSE.toString(), "0");
});

test("input validation", () => {
  assert.throws(() => fitSegmentedRegression([0, 1], [0], 1), /same length/);
  assert.throws(() => fitSegmentedRegression([0, 1], [0, 1], 1), /at least 3/);
  assert.throws(() => fitSegmentedRegression([0, 1, 2], [0, 1, 2], 0), /positive integer/);
  assert.throws(
    () => fitSegmentedRegression([0, 1, 2, 3, 4], [0, 1, 2, 3, 4], 2),
    /infeasible/,
  );
  assert.throws(() => fitSegmentedRegression([0, 1.5, 2], [0, 1, 2], 1), /integers/);
});

test("minSegmentLength is configurable", () => {
  const xs = [0, 1, 2, 3];
  const ys = [0, 1, 4, 3];
  const r = fitSegmentedRegression(xs, ys, 2, 2);
  assert.equal(r.segments.length, 2);
  assert.deepEqual(r.breakpoints, [2]);
  assert.equal(r.totalSSE.toString(), "0");
});
