import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { ErrorCodes } from "../src/errors.ts";
import { aggregateSnapshots, exportJSON } from "../src/metrics/exporter.ts";
import { MetricsRegistry } from "../src/metrics/registry.ts";
import { MetricKind } from "../src/metrics/types.ts";

/**
 * Concurrency safety: all registry mutations are synchronous, so interleaved
 * async tasks must produce exact totals with no lost updates and no leaks.
 */
describe("metrics concurrency safety", () => {
  it("counts exactly under 10k interleaved async increments", async () => {
    const r = new MetricsRegistry();
    const tasks: Promise<void>[] = [];
    for (let i = 0; i < 100; i++) {
      tasks.push((async () => {
        for (let j = 0; j < 100; j++) {
          await Promise.resolve(); // force interleaving
          r.counter("ops", { worker: String(i % 4) }).inc();
        }
      })());
    }
    await Promise.all(tasks);
    let total = 0;
    for (let w = 0; w < 4; w++) total += r.counter("ops", { worker: String(w) }).get();
    assert.equal(total, 10_000);
    assert.equal(r.size, 4); // exactly four series, no duplicates
  });

  it("timers record exactly one sample per concurrent async operation", async () => {
    const r = new MetricsRegistry();
    const timer = r.timer("op.duration_ms");
    await Promise.all(
      Array.from({ length: 200 }, (_, i) =>
        timer.timeAsync(async () => {
          await new Promise((res) => setTimeout(res, i % 5));
        }),
      ),
    );
    const h = r.histogram("op.duration_ms");
    assert.equal(h.count, 200);
    assert.ok(h.totalSum > 0);
    assert.ok(h.percentile(50) >= 0);
  });

  it("concurrent error recording keeps exact per-code counts", async () => {
    const r = new MetricsRegistry();
    const codes = [ErrorCodes.WAL_TORN_WRITE, ErrorCodes.RECOVERY_INVALID_LSN, ErrorCodes.BUFFER_PIN_VIOLATION];
    await Promise.all(
      Array.from({ length: 300 }, (_, i) => Promise.resolve().then(() => r.recordError(codes[i % 3]!))),
    );
    for (const code of codes) {
      assert.equal(r.counter("errors.total", { code }).get(), 100);
    }
  });

  it("snapshots taken during concurrent mutation are internally consistent", async () => {
    const r = new MetricsRegistry();
    const stop = { done: false };
    const writer = (async () => {
      while (!stop.done) {
        r.counter("hits").inc();
        await Promise.resolve();
      }
    })();
    for (let i = 0; i < 50; i++) {
      const snaps = r.snapshots();
      const counter = snaps.find((s) => s.name === "hits");
      if (counter !== undefined) {
        assert.ok(counter.kind === MetricKind.Counter && counter.value >= 0);
      }
      await Promise.resolve();
    }
    stop.done = true;
    await writer;
    const final = r.counter("hits").get();
    r.reset();
    assert.equal(r.size, 0);
    assert.ok(final > 0);
  });

  it("aggregation of concurrently-produced registries is exact", async () => {
    const registries = Array.from({ length: 8 }, () => new MetricsRegistry());
    await Promise.all(
      registries.map(async (r, idx) => {
        for (let i = 0; i <= idx; i++) {
          r.counter("work").inc();
          r.histogram("latency").observe(i + 1);
        }
        await Promise.resolve();
      }),
    );
    const merged = aggregateSnapshots(registries.map((r) => r.snapshots()));
    const work = merged.find((m) => m.name === "work");
    assert.ok(work !== undefined && work.kind === MetricKind.Counter);
    assert.equal(work.value, 36); // sum(1..8)
    const latency = merged.find((m) => m.name === "latency");
    assert.ok(latency !== undefined && latency.kind === MetricKind.Histogram);
    assert.equal(latency.count, 36);
    assert.equal(latency.sum, 120); // sum_{k=1..8} k(k+1)/2
  });

  it("export after concurrent load leaves no residual series after reset", async () => {
    const r = new MetricsRegistry();
    await Promise.all(
      Array.from({ length: 50 }, (_, i) =>
        Promise.resolve().then(() => {
          r.counter("c", { i: String(i) }).inc();
          r.gauge("g", { i: String(i) }).set(i);
          r.histogram("h", { i: String(i) }).observe(i);
        }),
      ),
    );
    const bundle = exportJSON(r.snapshots());
    assert.equal(bundle.metrics.length, 150);
    r.reset();
    assert.equal(r.snapshots().length, 0);
    assert.equal(r.size, 0);
  });
});
