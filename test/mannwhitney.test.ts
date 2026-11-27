import { test } from "node:test";
import assert from "node:assert/strict";
import { mannWhitneyU, midRanks, normalCdf } from "../src/stats.js";

test("mid-ranks assign average ranks to ties", () => {
  // values: 10,10,10,10,20,20,20,30 -> ranks 2.5 x4, 6 x3, 8
  assert.deepEqual(
    midRanks([10, 10, 10, 10, 20, 20, 20, 30]),
    [2.5, 2.5, 2.5, 2.5, 6, 6, 6, 8],
  );
});

test("tie-heavy samples: U and sigma_U match exact hand calculation", () => {
  // baseline = [10,10,10,20], canary = [10,20,20,30]; n1 = n2 = 4, N = 8
  // combined sorted: 10x4 (rank 2.5), 20x3 (rank 6), 30 (rank 8)
  // R_canary = 2.5 + 6 + 6 + 8 = 22.5 -> U_canary = 22.5 - 10 = 12.5
  // U_baseline = 16 - 12.5 = 3.5
  // sum(t^3 - t) = (64-4) + (27-3) = 84; N(N-1) = 56
  // sigma = sqrt(16/12 * (9 - 84/56)) = sqrt(16/12 * 7.5) = sqrt(10)
  const r = mannWhitneyU([10, 10, 10, 20], [10, 20, 20, 30]);
  assert.equal(r.uCanary, 12.5);
  assert.equal(r.uBaseline, 3.5);
  assert.ok(Math.abs(r.sigmaU - Math.sqrt(10)) < 1e-12);
  const expectedZ = 4.5 / Math.sqrt(10); // (12.5 - 8) / sqrt(10) ~ 1.42302
  assert.ok(Math.abs(r.z - expectedZ) < 1e-12);
  // Standard normal table: Phi(1.4230) ~ 0.9226
  assert.ok(Math.abs(r.pUpper - 0.0774) < 5e-4, `pUpper=${r.pUpper}`);
  assert.ok(Math.abs(r.pLower - 0.9226) < 5e-4, `pLower=${r.pLower}`);
  assert.ok(Math.abs(r.pTwoSided - 0.1547) < 1e-3, `pTwoSided=${r.pTwoSided}`);
});

test("no-tie case matches the naive (no-tie) variance formula", () => {
  // baseline = [1,2,3], canary = [4,5,6]: U_canary = 9, sigma = sqrt(3*3*7/12)
  const r = mannWhitneyU([1, 2, 3], [4, 5, 6]);
  assert.equal(r.uCanary, 9);
  assert.equal(r.uBaseline, 0);
  assert.ok(Math.abs(r.sigmaU - Math.sqrt(5.25)) < 1e-12);
  assert.ok(Math.abs(r.z - 4.5 / Math.sqrt(5.25)) < 1e-12);
});

test("all-identical samples: sigma_U is 0, zero difference, no NaN", () => {
  const r = mannWhitneyU([5, 5, 5, 5], [5, 5, 5]);
  assert.equal(r.sigmaU, 0);
  assert.equal(r.z, 0);
  assert.equal(r.pTwoSided, 1);
  for (const v of [r.z, r.pUpper, r.pLower, r.pTwoSided, r.uCanary]) {
    assert.ok(Number.isFinite(v), "must never produce NaN/Infinity");
  }
});

test("discrete error-rate-like data (many zeros/ones) uses tie correction", () => {
  // 20 zeros and 5 ones per side: massive ties. Tie-corrected sigma must be
  // strictly smaller than the naive no-tie sigma.
  const baseline = [...Array(20).fill(0), ...Array(5).fill(1)];
  const canary = [...Array(20).fill(0), ...Array(5).fill(1)];
  const r = mannWhitneyU(baseline, canary);
  const naiveSigma = Math.sqrt((25 * 25 * (50 + 1)) / 12);
  assert.ok(r.sigmaU < naiveSigma);
  assert.ok(r.sigmaU > 0);
  // Identical distributions -> z = 0 exactly.
  assert.equal(r.z, 0);
});

test("normalCdf sanity", () => {
  assert.ok(Math.abs(normalCdf(0) - 0.5) < 1e-7);
  assert.ok(Math.abs(normalCdf(1.96) - 0.9750021) < 1e-5);
  assert.ok(Math.abs(normalCdf(-1.645) - 0.0499849) < 1e-4);
});
