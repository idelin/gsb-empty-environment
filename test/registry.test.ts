import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { EngineError, ErrorCodes } from "../src/errors.ts";
import { MetricsRegistry } from "../src/metrics/registry.ts";

describe("MetricsRegistry", () => {
  it("returns the same series for identical name+labels", () => {
    const r = new MetricsRegistry();
    const a = r.counter("hits", { page: "1" });
    const b = r.counter("hits", { page: "1" });
    assert.equal(a, b);
    a.inc();
    assert.equal(b.get(), 1);
  });

  it("canonicalizes label order", () => {
    const r = new MetricsRegistry();
    const a = r.counter("hits", { b: "2", a: "1" });
    const b = r.counter("hits", { a: "1", b: "2" });
    assert.equal(a, b);
    assert.equal(r.size, 1);
  });

  it("separates series by label values", () => {
    const r = new MetricsRegistry();
    r.counter("hits", { page: "1" });
    r.counter("hits", { page: "2" });
    assert.equal(r.size, 2);
  });

  it("rejects kind conflicts on the same series key", () => {
    const r = new MetricsRegistry();
    r.counter("hits", { page: "1" });
    assert.throws(
      () => r.gauge("hits", { page: "1" }),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.METRIC_TYPE_CONFLICT,
    );
  });

  it("rejects illegal metric names", () => {
    const r = new MetricsRegistry();
    for (const bad of ["", "1abc", "has space", "dash-name"]) {
      assert.throws(() => r.counter(bad), (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.METRIC_INVALID_VALUE);
    }
  });

  it("records error codes under errors.total{code}", () => {
    const r = new MetricsRegistry();
    r.recordError(ErrorCodes.WAL_TORN_WRITE);
    r.recordError(ErrorCodes.WAL_TORN_WRITE);
    r.recordError(ErrorCodes.RECOVERY_INVALID_LSN);
    assert.equal(r.counter("errors.total", { code: ErrorCodes.WAL_TORN_WRITE }).get(), 2);
    assert.equal(r.counter("errors.total", { code: ErrorCodes.RECOVERY_INVALID_LSN }).get(), 1);
  });

  it("trackErrors records EngineError codes and rethrows", () => {
    const r = new MetricsRegistry();
    assert.throws(
      () => r.trackErrors(() => { throw new EngineError(ErrorCodes.BUFFER_PAGE_NOT_FOUND, "x"); }),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.BUFFER_PAGE_NOT_FOUND,
    );
    assert.equal(r.counter("errors.total", { code: ErrorCodes.BUFFER_PAGE_NOT_FOUND }).get(), 1);
  });

  it("trackErrors does not record non-engine errors", () => {
    const r = new MetricsRegistry();
    assert.throws(() => r.trackErrors(() => { throw new TypeError("nope"); }), TypeError);
    assert.equal(r.size, 0);
  });

  it("snapshots are deep copies detached from live series", () => {
    const r = new MetricsRegistry();
    const c = r.counter("hits", { page: "1" });
    c.inc(5);
    const snaps = r.snapshots();
    const snap = snaps[0]!;
    c.inc(100);
    assert.equal(snap.value, 5);
    (snap as { value: number }).value = 999;
    snap.labels.page = "hacked";
    assert.equal(c.get(), 105);
    assert.equal(r.counter("hits", { page: "1" }).get(), 105);
  });

  it("reset clears every series leaving no references", () => {
    const r = new MetricsRegistry();
    r.counter("a").inc();
    r.gauge("b").set(1);
    r.histogram("c").observe(1);
    assert.equal(r.size, 3);
    r.reset();
    assert.equal(r.size, 0);
    assert.deepEqual(r.snapshots(), []);
    // registry is reusable after reset
    r.counter("a").inc();
    assert.equal(r.counter("a").get(), 1);
  });
});
