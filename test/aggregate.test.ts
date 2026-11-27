import { test } from "node:test";
import assert from "node:assert/strict";
import {
  aggregateMetrics,
  type AggregateConfig,
  type MetricSamples,
} from "../src/aggregate.js";

function samples(map: Record<string, MetricSamples>): Map<string, MetricSamples> {
  return new Map(Object.entries(map));
}

const passing = { baseline: [1, 2, 3, 4, 5], canary: [1, 2, 3, 4, 5] };
const failing = {
  baseline: Array.from({ length: 40 }, (_, i) => 100 + (i % 5)),
  canary: Array.from({ length: 40 }, (_, i) => 200 + (i % 5)),
};

test("group weights and intra-group weights are normalized", () => {
  // g1 (w=2): m1 PASS (w=1), m2 FAIL (w=3) -> group score 1/4 = 0.25
  // g2 (w=1): m3 PASS -> 1.0
  // total = (2*0.25 + 1*1.0) / 3 = 0.5
  const config: AggregateConfig = {
    passScore: 0.9,
    groups: [
      {
        name: "g1",
        weight: 2,
        metrics: [
          { name: "m1", direction: "failOnHigh", weight: 1 },
          { name: "m2", direction: "failOnHigh", weight: 3 },
        ],
      },
      {
        name: "g2",
        weight: 1,
        metrics: [{ name: "m3", direction: "failOnHigh" }],
      },
    ],
  };
  const r = aggregateMetrics(
    config,
    samples({ m1: passing, m2: failing, m3: passing }),
  );
  assert.ok(Math.abs(r.score - 0.5) < 1e-12, `score=${r.score}`);
  assert.equal(r.verdict, "FAIL");
});

test("critical metric vetoes even when total score passes", () => {
  // m1 critical FAIL in a tiny group; m2/m3 PASS in a dominant group.
  // score = 0.1*0 + 0.9*1 = 0.9 >= passScore 0.8, but veto must win.
  const config: AggregateConfig = {
    passScore: 0.8,
    groups: [
      {
        name: "g1",
        weight: 1,
        metrics: [
          { name: "m1", direction: "failOnHigh", critical: true },
        ],
      },
      {
        name: "g2",
        weight: 9,
        metrics: [
          { name: "m2", direction: "failOnHigh" },
          { name: "m3", direction: "failOnHigh" },
        ],
      },
    ],
  };
  const r = aggregateMetrics(
    config,
    samples({ m1: failing, m2: passing, m3: passing }),
  );
  assert.equal(r.verdict, "FAIL");
  assert.equal(r.vetoedBy, "m1");
  assert.match(r.reason, /veto/);
});

test("NODATA_FAIL metric vetoes regardless of score", () => {
  const config: AggregateConfig = {
    passScore: 0.5,
    groups: [
      {
        name: "g1",
        weight: 1,
        metrics: [
          { name: "m1", direction: "failOnHigh", noDataPolicy: "NODATA_FAIL" },
        ],
      },
      {
        name: "g2",
        weight: 1,
        metrics: [{ name: "m2", direction: "failOnHigh" }],
      },
    ],
  };
  const r = aggregateMetrics(
    config,
    samples({ m1: { baseline: [], canary: [] }, m2: passing }),
  );
  assert.equal(r.verdict, "FAIL");
  assert.equal(r.vetoedBy, "m1");
});

test("NODATA_PASS metrics are dropped and remaining weights re-normalized", () => {
  // g1 (w=1): m1 PASS; g2 (w=1): m2 has no data with NODATA_PASS.
  // g2 becomes empty -> excluded; score = 1.0 from g1 alone.
  const config: AggregateConfig = {
    passScore: 0.9,
    groups: [
      {
        name: "g1",
        weight: 1,
        metrics: [{ name: "m1", direction: "failOnHigh" }],
      },
      {
        name: "g2",
        weight: 1,
        metrics: [
          { name: "m2", direction: "failOnHigh", noDataPolicy: "NODATA_PASS" },
        ],
      },
    ],
  };
  const r = aggregateMetrics(
    config,
    samples({ m1: passing, m2: { baseline: [], canary: [] } }),
  );
  assert.equal(r.score, 1);
  assert.equal(r.verdict, "PASS");
  const m2 = r.metricResults.find((m) => m.name === "m2")!;
  assert.equal(m2.verdict, "SKIPPED");
});

test("intra-group re-normalization when one metric is skipped", () => {
  // g1 (w=1): m1 PASS (w=1), m2 SKIPPED (w=3) -> group score 1.0 (not 0.25)
  const config: AggregateConfig = {
    passScore: 0.9,
    groups: [
      {
        name: "g1",
        weight: 1,
        metrics: [
          { name: "m1", direction: "failOnHigh", weight: 1 },
          {
            name: "m2",
            direction: "failOnHigh",
            weight: 3,
            noDataPolicy: "NODATA_PASS",
          },
        ],
      },
    ],
  };
  const r = aggregateMetrics(
    config,
    samples({ m1: passing, m2: { baseline: [], canary: [] } }),
  );
  assert.equal(r.score, 1);
  assert.equal(r.verdict, "PASS");
});

test("inconclusive metric yields INCONCLUSIVE release verdict", () => {
  const config: AggregateConfig = {
    passScore: 0.9,
    groups: [
      {
        name: "g1",
        weight: 1,
        metrics: [
          { name: "m1", direction: "failOnHigh" },
          { name: "m2", direction: "failOnHigh", minSampleSize: 100 },
        ],
      },
    ],
  };
  const r = aggregateMetrics(
    config,
    samples({ m1: passing, m2: passing }),
  );
  assert.equal(r.verdict, "INCONCLUSIVE");
  assert.equal(r.score, 1); // m2 excluded from scoring, m1 passes
});

test("all-pass release passes", () => {
  const config: AggregateConfig = {
    passScore: 0.95,
    groups: [
      {
        name: "g1",
        weight: 1,
        metrics: [
          { name: "m1", direction: "failOnHigh" },
          { name: "m2", direction: "failOnLow" },
        ],
      },
    ],
  };
  const r = aggregateMetrics(config, samples({ m1: passing, m2: passing }));
  assert.equal(r.verdict, "PASS");
  assert.equal(r.score, 1);
});
