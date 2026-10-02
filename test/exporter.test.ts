import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { EngineError, ErrorCodes } from "../src/errors.ts";
import {
  aggregateSnapshots,
  deriveRatios,
  estimatePercentiles,
  exportJSON,
  exportPrometheus,
} from "../src/metrics/exporter.ts";
import { MetricsRegistry } from "../src/metrics/registry.ts";
import { MetricKind, type HistogramSnapshot, type MetricSnapshot } from "../src/metrics/types.ts";

function counterSnap(name: string, value: number, labels: Record<string, string> = {}): MetricSnapshot {
  return { kind: MetricKind.Counter, name, labels, value };
}

function histSnap(count: number, sum: number, bucketCounts: number[]): HistogramSnapshot {
  return {
    kind: MetricKind.Histogram,
    name: "latency",
    labels: {},
    count,
    sum,
    min: count === 0 ? 0 : 1,
    max: count === 0 ? 0 : 100,
    buckets: [10, 100],
    bucketCounts,
    percentiles: {},
  };
}

describe("exportJSON", () => {
  it("emits generatedAt, metrics and derived ratios", () => {
    const r = new MetricsRegistry();
    r.counter("buffer.hit").inc(3);
    r.counter("buffer.miss").inc(1);
    const bundle = exportJSON(r.snapshots());
    assert.ok(typeof bundle.generatedAt === "string");
    assert.equal(bundle.metrics.length, 2);
    assert.equal(bundle.derived["buffer.hit_rate"], 0.75);
  });

  it("derived hit rate is NaN with zero lookups", () => {
    const derived = deriveRatios([]);
    assert.ok(Number.isNaN(derived["buffer.hit_rate"]));
    assert.ok(Number.isNaN(derived["recovery.redo.apply_rate"]));
    assert.equal(derived["errors.total"], 0);
  });

  it("sums errors.total across label sets", () => {
    const snaps = [
      counterSnap("errors.total", 2, { code: "E_A" }),
      counterSnap("errors.total", 3, { code: "E_B" }),
    ];
    assert.equal(deriveRatios(snaps)["errors.total"], 5);
  });
});

describe("exportPrometheus", () => {
  it("renders counters, gauges and histograms in exposition format", () => {
    const r = new MetricsRegistry();
    r.counter("requests.total", { path: "/a" }).inc(7);
    r.gauge("inflight").set(2);
    const h = r.histogram("op.seconds", {}, [0.1, 1]);
    h.observe(0.05);
    h.observe(0.5);
    const text = exportPrometheus(r.snapshots());
    assert.match(text, /# TYPE requests_total counter\nrequests_total\{path="\/a"\} 7/);
    assert.match(text, /# TYPE inflight gauge\ninflight 2/);
    assert.match(text, /op_seconds_bucket\{le="0.1"\} 1/);
    assert.match(text, /op_seconds_bucket\{le="1"\} 2/);
    assert.match(text, /op_seconds_bucket\{le="\+Inf"\} 2/);
    assert.match(text, /op_seconds_sum 0.55/);
    assert.match(text, /op_seconds_count 2/);
  });

  it("escapes quotes and backslashes in label values", () => {
    const text = exportPrometheus([counterSnap("weird", 1, { msg: 'a"b\\c' })]);
    assert.match(text, /weird\{msg="a\\"b\\\\c"\} 1/);
  });
});

describe("aggregateSnapshots", () => {
  it("sums counters across batches with identical identity", () => {
    const merged = aggregateSnapshots([
      [counterSnap("hits", 3, { p: "1" })],
      [counterSnap("hits", 4, { p: "1" }), counterSnap("hits", 1, { p: "2" })],
    ]);
    const byKey = new Map(merged.map((m) => [`${m.name}|${JSON.stringify(m.labels)}`, m]));
    assert.equal((byKey.get(`hits|{"p":"1"}`) as { value: number }).value, 7);
    assert.equal((byKey.get(`hits|{"p":"2"}`) as { value: number }).value, 1);
  });

  it("merges histograms: count/sum/min/max and bucket counts", () => {
    const merged = aggregateSnapshots([
      [histSnap(2, 30, [1, 2, 0])],
      [histSnap(3, 70, [0, 2, 1])],
    ]);
    assert.equal(merged.length, 1);
    const h = merged[0] as HistogramSnapshot;
    assert.equal(h.count, 5);
    assert.equal(h.sum, 100);
    assert.deepEqual(h.bucketCounts, [1, 4, 1]);
    assert.ok(!Number.isNaN(h.percentiles.p50!));
  });

  it("handles empty batches and empty input", () => {
    assert.deepEqual(aggregateSnapshots([]), []);
    assert.deepEqual(aggregateSnapshots([[], []]), []);
  });

  it("rejects kind conflicts and bucket mismatches", () => {
    assert.throws(
      () => aggregateSnapshots([[counterSnap("x", 1)], [{ kind: MetricKind.Gauge, name: "x", labels: {}, value: 1 }]]),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.METRIC_TYPE_CONFLICT,
    );
    const a = histSnap(1, 5, [1, 0, 0]);
    const b = { ...histSnap(1, 5, [1, 0, 0]), buckets: [5, 50] };
    assert.throws(
      () => aggregateSnapshots([[a], [b]]),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.METRIC_INVALID_VALUE,
    );
  });

  it("does not mutate the input snapshots", () => {
    const original = counterSnap("hits", 3);
    aggregateSnapshots([[original], [counterSnap("hits", 4)]]);
    assert.equal((original as { value: number }).value, 3);
  });
});

describe("estimatePercentiles", () => {
  it("returns NaN for empty histograms", () => {
    const ps = estimatePercentiles([10, 100], [0, 0, 0]);
    assert.ok(Number.isNaN(ps.p50));
  });

  it("interpolates within the matched bucket", () => {
    // 10 samples all in (0,10]: p50 estimate = 5
    const ps = estimatePercentiles([10, 100], [10, 10, 10], [50, 100]);
    assert.equal(ps.p50, 5);
    assert.equal(ps.p100, 10);
  });
});
