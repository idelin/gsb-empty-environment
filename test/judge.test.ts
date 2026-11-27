import { test } from "node:test";
import assert from "node:assert/strict";
import { judgeMetric, type MetricConfig } from "../src/judge.js";

const baseConfig: MetricConfig = {
  name: "latency",
  direction: "failOnHigh",
  alpha: 0.05,
};

test("significant degradation beyond minEffectRatio fails", () => {
  const baseline = Array.from({ length: 50 }, (_, i) => 100 + (i % 7));
  const canary = Array.from({ length: 50 }, (_, i) => 120 + (i % 7));
  const r = judgeMetric(
    { ...baseConfig, minEffectRatio: 1.05 },
    baseline,
    canary,
  );
  assert.equal(r.verdict, "FAIL");
  assert.ok(r.effectRatio! > 1.05);
  assert.ok(r.stats!.pUpper < 0.05);
});

test("huge sample with only 0.2% degradation is waived by effect-size gate", () => {
  // n = 5000 per side; canary is deterministically 0.2% slower.
  // U is maximally significant (p ~ 0) yet the ratio 1.002 < 1.01 must pass.
  const n = 5000;
  const baseline = Array.from({ length: n }, () => 100);
  const canary = Array.from({ length: n }, () => 100.2);
  const r = judgeMetric(
    { ...baseConfig, minEffectRatio: 1.01 },
    baseline,
    canary,
  );
  assert.ok(r.stats!.pUpper < 1e-10, "must be statistically significant");
  assert.ok(Math.abs(r.effectRatio! - 1.002) < 1e-12);
  assert.equal(r.verdict, "PASS");
  assert.match(r.reason, /effect-size gate/);
});

test("without effect gate (minEffectRatio=1) the 0.2% regression fails", () => {
  const n = 5000;
  const baseline = Array.from({ length: n }, () => 100);
  const canary = Array.from({ length: n }, () => 100.2);
  const r = judgeMetric(baseConfig, baseline, canary);
  assert.equal(r.verdict, "FAIL");
});

test("failOnLow direction detects significant drops", () => {
  const baseline = Array.from({ length: 60 }, (_, i) => 50 + (i % 5));
  const canary = Array.from({ length: 60 }, (_, i) => 40 + (i % 5));
  const r = judgeMetric(
    { name: "throughput", direction: "failOnLow", minEffectRatio: 1.05 },
    baseline,
    canary,
  );
  assert.equal(r.verdict, "FAIL");
  assert.ok(r.effectRatio! > 1.05);
});

test("failOnLow passes when canary is not lower", () => {
  const baseline = Array.from({ length: 60 }, (_, i) => 50 + (i % 5));
  const canary = Array.from({ length: 60 }, (_, i) => 52 + (i % 5));
  const r = judgeMetric(
    { name: "throughput", direction: "failOnLow" },
    baseline,
    canary,
  );
  assert.equal(r.verdict, "PASS");
});

test("all-identical samples pass with zero difference and no NaN", () => {
  const r = judgeMetric(baseConfig, [7, 7, 7, 7], [7, 7, 7]);
  assert.equal(r.verdict, "PASS");
  assert.equal(r.stats!.sigmaU, 0);
  assert.ok(Number.isFinite(r.stats!.z));
  assert.match(r.reason, /zero difference/);
});

test("empty sample with NODATA_FAIL fails the metric", () => {
  const r = judgeMetric(
    { ...baseConfig, noDataPolicy: "NODATA_FAIL" },
    [],
    [1, 2, 3],
  );
  assert.equal(r.verdict, "FAIL");
  assert.match(r.reason, /NODATA_FAIL/);
});

test("empty sample with NODATA_PASS skips the metric", () => {
  const r = judgeMetric(
    { ...baseConfig, noDataPolicy: "NODATA_PASS" },
    [1, 2, 3],
    [],
  );
  assert.equal(r.verdict, "SKIPPED");
});

test("sample below minSampleSize is INCONCLUSIVE (retry, not rollback)", () => {
  const r = judgeMetric(
    { ...baseConfig, minSampleSize: 10 },
    [1, 2, 3],
    [1, 2, 3, 4, 5, 6, 7, 8, 9, 10],
  );
  assert.equal(r.verdict, "INCONCLUSIVE");
  assert.match(r.reason, /retry/);
});

test("non-significant difference passes", () => {
  const baseline = [1, 5, 2, 6, 3, 7, 4, 8];
  const canary = [1, 5, 2, 6, 3, 7, 4, 9];
  const r = judgeMetric(baseConfig, baseline, canary);
  assert.equal(r.verdict, "PASS");
});
