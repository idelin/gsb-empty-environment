import { test } from "node:test";
import assert from "node:assert/strict";
import {
  chiSquarePValue,
  chiSquareUniform,
  gammaQ,
  logGamma,
  mean,
  minMax,
  stddev,
} from "../src/stats.ts";

test("mean and stddev on known data", () => {
  const xs = [2, 4, 4, 4, 5, 5, 7, 9];
  assert.equal(mean(xs), 5);
  // Population stddev of this classic example is exactly 2.
  assert.equal(stddev(xs), 2);
});

test("minMax", () => {
  assert.deepEqual(minMax([3, -1, 7, 2]), { min: -1, max: 7 });
});

test("chiSquareUniform is zero for a perfectly uniform histogram", () => {
  const { stat, df, expected } = chiSquareUniform([10, 10, 10, 10]);
  assert.equal(stat, 0);
  assert.equal(df, 3);
  assert.equal(expected, 10);
});

test("chiSquareUniform hand-computed value", () => {
  // observed [12, 8], expected 10 each: (2^2 + 2^2)/10 = 0.8
  const { stat, df } = chiSquareUniform([12, 8]);
  assert.equal(df, 1);
  assert.ok(Math.abs(stat - 0.8) < 1e-12);
});

test("logGamma matches known values", () => {
  // Gamma(5) = 24, Gamma(0.5) = sqrt(pi)
  assert.ok(Math.abs(logGamma(5) - Math.log(24)) < 1e-10);
  assert.ok(Math.abs(logGamma(0.5) - Math.log(Math.sqrt(Math.PI))) < 1e-10);
});

test("gammaQ(1, x) equals exp(-x)", () => {
  for (const x of [0.5, 1, 2.5, 10]) {
    assert.ok(Math.abs(gammaQ(1, x) - Math.exp(-x)) < 1e-10);
  }
});

test("chiSquarePValue hits textbook critical values", () => {
  // chi2 = 3.8414588 with df=1 -> p = 0.05
  assert.ok(Math.abs(chiSquarePValue(3.8414588, 1) - 0.05) < 1e-4);
  // chi2 = 16.91898 with df=9 -> p = 0.05
  assert.ok(Math.abs(chiSquarePValue(16.91898, 9) - 0.05) < 1e-4);
  // chi2 = 0 -> p = 1
  assert.equal(chiSquarePValue(0, 5), 1);
});
