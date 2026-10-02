import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { EngineError, ErrorCodes } from "../src/errors.ts";
import { Counter, Gauge, Histogram } from "../src/metrics/primitives.ts";
import { Timer } from "../src/metrics/timer.ts";

describe("Counter", () => {
  it("increments by 1 by default and by explicit deltas", () => {
    const c = new Counter("requests", {});
    c.inc();
    c.inc(4);
    assert.equal(c.get(), 5);
  });

  it("rejects negative, NaN and infinite deltas", () => {
    const c = new Counter("requests", {});
    for (const bad of [-1, Number.NaN, Number.POSITIVE_INFINITY]) {
      assert.throws(() => c.inc(bad), (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.METRIC_INVALID_VALUE);
    }
    assert.equal(c.get(), 0);
  });

  it("resets to zero without residual state", () => {
    const c = new Counter("requests", {});
    c.inc(10);
    c.reset();
    assert.equal(c.get(), 0);
    assert.equal(c.snapshot().value, 0);
  });
});

describe("Gauge", () => {
  it("supports set/inc/dec including negatives", () => {
    const g = new Gauge("inflight", {});
    g.set(5);
    g.inc(3);
    g.dec(10);
    assert.equal(g.get(), -2);
  });

  it("rejects non-finite values", () => {
    const g = new Gauge("inflight", {});
    assert.throws(() => g.set(Number.NaN), EngineError);
    assert.throws(() => g.inc(Number.POSITIVE_INFINITY), EngineError);
  });
});

describe("Histogram", () => {
  it("reports NaN percentiles and zeroed stats when empty", () => {
    const h = new Histogram("latency", {}, [1, 10, 100]);
    assert.equal(h.count, 0);
    assert.ok(Number.isNaN(h.percentile(50)));
    const snap = h.snapshot();
    assert.equal(snap.count, 0);
    assert.equal(snap.sum, 0);
    assert.equal(snap.min, 0);
    assert.equal(snap.max, 0);
    assert.ok(Number.isNaN(snap.percentiles.p99));
  });

  it("tracks count/sum/min/max exactly", () => {
    const h = new Histogram("latency", {}, [1, 10, 100]);
    for (const v of [5, 15, 25, 35]) h.observe(v);
    assert.equal(h.count, 4);
    assert.equal(h.totalSum, 80);
    const snap = h.snapshot();
    assert.equal(snap.min, 5);
    assert.equal(snap.max, 35);
  });

  it("places boundary values into their exact le bucket (cumulative)", () => {
    const h = new Histogram("latency", {}, [1, 10, 100]);
    h.observe(1);   // exactly bucket 0
    h.observe(10);  // exactly bucket 1
    h.observe(101); // +Inf bucket
    assert.deepEqual(h.cumulativeBuckets(), [1, 2, 2, 3]);
  });

  it("computes exact percentiles from retained samples", () => {
    const h = new Histogram("latency", {}, [1000]);
    for (let i = 1; i <= 100; i++) h.observe(i);
    assert.equal(h.percentile(50), 50);
    assert.equal(h.percentile(90), 90);
    assert.equal(h.percentile(99), 99);
    assert.equal(h.percentile(100), 100);
    assert.equal(h.percentile(0), 1);
  });

  it("rejects invalid observations and percentile arguments", () => {
    const h = new Histogram("latency", {}, [1]);
    assert.throws(() => h.observe(-0.5), EngineError);
    assert.throws(() => h.observe(Number.NaN), EngineError);
    h.observe(1);
    assert.throws(() => h.percentile(-1), EngineError);
    assert.throws(() => h.percentile(101), EngineError);
  });

  it("rejects empty, duplicate or negative bucket layouts", () => {
    assert.throws(() => new Histogram("h", {}, []), EngineError);
    assert.throws(() => new Histogram("h", {}, [1, 1]), EngineError);
    assert.throws(() => new Histogram("h", {}, [-5]), EngineError);
  });

  it("sorts unsorted bucket input", () => {
    const h = new Histogram("h", {}, [100, 1, 10]);
    assert.deepEqual(h.buckets, [1, 10, 100]);
  });

  it("reset drops all samples (no retained memory)", () => {
    const h = new Histogram("latency", {}, [1, 10]);
    for (let i = 0; i < 1000; i++) h.observe(i);
    h.reset();
    assert.equal(h.count, 0);
    assert.ok(Number.isNaN(h.percentile(50)));
    assert.deepEqual(h.cumulativeBuckets(), [0, 0, 0]);
  });
});

describe("Timer", () => {
  it("records one sample per start/stop pair", () => {
    const h = new Histogram("op_ms", {}, [1000]);
    const t = new Timer(h);
    const stop = t.start();
    const elapsed = stop();
    assert.ok(elapsed >= 0);
    assert.equal(h.count, 1);
  });

  it("throws when the stop closure is invoked twice", () => {
    const t = new Timer(new Histogram("op_ms", {}, [1000]));
    const stop = t.start();
    stop();
    assert.throws(() => stop(), (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.METRIC_INVALID_VALUE);
  });

  it("records even when the timed function throws", () => {
    const h = new Histogram("op_ms", {}, [1000]);
    const t = new Timer(h);
    assert.throws(() => t.time(() => { throw new Error("boom"); }), /boom/);
    assert.equal(h.count, 1);
  });

  it("records even when the async function rejects", async () => {
    const h = new Histogram("op_ms", {}, [1000]);
    const t = new Timer(h);
    await assert.rejects(() => t.timeAsync(async () => { throw new Error("boom"); }), /boom/);
    assert.equal(h.count, 1);
  });

  it("measures real elapsed time for async work", async () => {
    const h = new Histogram("op_ms", {}, [1000]);
    const t = new Timer(h);
    await t.timeAsync(() => new Promise((r) => setTimeout(r, 15)));
    assert.ok(h.percentile(50) >= 10, `expected >= 10ms, got ${h.percentile(50)}`);
  });
});
