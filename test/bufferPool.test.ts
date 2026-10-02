import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { EngineError, ErrorCodes } from "../src/errors.ts";
import { MetricsRegistry } from "../src/metrics/registry.ts";
import { BufferPool } from "../src/storage/bufferPool.ts";
import { DiskManager } from "../src/storage/disk.ts";

function setup(capacity = 4) {
  const dir = mkdtempSync(join(tmpdir(), "bp-test-"));
  const metrics = new MetricsRegistry();
  const disk = new DiskManager(join(dir, "data.db"), 4096, 16, metrics);
  const pool = new BufferPool(disk, metrics, capacity);
  return { metrics, disk, pool };
}

describe("BufferPool", () => {
  it("counts miss on first fetch and hit on subsequent fetches", () => {
    const { metrics, pool } = setup();
    pool.getPage(0);
    pool.unpin(0);
    pool.getPage(0);
    pool.unpin(0);
    assert.equal(metrics.counter("buffer.miss").get(), 1);
    assert.equal(metrics.counter("buffer.hit").get(), 1);
  });

  it("updatePage marks the frame dirty and stamps pageLSN", () => {
    const { pool } = setup();
    pool.updatePage(0, "hello", 42);
    const frame = pool.getPage(0);
    assert.equal(frame.data, "hello");
    assert.equal(frame.pageLSN, 42);
    assert.equal(frame.dirty, true);
    pool.unpin(0);
  });

  it("recLSN tracks the first update after the last flush", () => {
    const { pool } = setup();
    pool.updatePage(0, "v1", 10);
    pool.updatePage(0, "v2", 20);
    assert.deepEqual([...pool.dirtyPages()], [[0, 10]]);
    pool.flushPage(0);
    assert.deepEqual([...pool.dirtyPages()], []);
    pool.updatePage(0, "v3", 30);
    assert.deepEqual([...pool.dirtyPages()], [[0, 30]]);
  });

  it("flush persists data and pageLSN to disk", () => {
    const { disk, pool } = setup();
    pool.updatePage(0, "persisted", 77);
    pool.flushAll();
    const page = disk.readPage(0);
    assert.equal(page.data, "persisted");
    assert.equal(page.pageLSN, 77);
  });

  it("unpin of an unknown page throws E_BUFFER_MISS", () => {
    const { pool } = setup();
    assert.throws(
      () => pool.unpin(99),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.BUFFER_PAGE_NOT_FOUND,
    );
  });

  it("pin count underflow throws E_BUFFER_PIN", () => {
    const { pool } = setup();
    pool.getPage(0);
    pool.unpin(0);
    assert.throws(
      () => pool.unpin(0),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.BUFFER_PIN_VIOLATION,
    );
  });

  it("evicts an unpinned frame when at capacity", () => {
    const { pool } = setup(2);
    pool.getPage(0); pool.unpin(0);
    pool.getPage(1); pool.unpin(1);
    pool.getPage(2); pool.unpin(2); // forces eviction of page 0
    assert.equal(pool.size, 2);
  });

  it("throws when every frame is pinned at capacity", () => {
    const { pool } = setup(1);
    pool.getPage(0); // stays pinned
    assert.throws(
      () => pool.getPage(1),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.BUFFER_PIN_VIOLATION,
    );
  });

  it("eviction flushes dirty frames to disk", () => {
    const { disk, pool } = setup(1);
    pool.updatePage(0, "dirty-data", 5);
    pool.getPage(1); // evicts page 0, must flush first
    pool.unpin(1);
    assert.equal(disk.readPage(0).data, "dirty-data");
  });

  it("assertQuiescent passes when clean and fails on dirty/pinned frames", () => {
    const { pool } = setup();
    pool.updatePage(0, "x", 1);
    assert.throws(() => pool.assertQuiescent(), EngineError); // dirty
    pool.flushAll();
    pool.assertQuiescent(); // clean now
    pool.getPage(1); // pinned
    assert.throws(() => pool.assertQuiescent(), EngineError);
    pool.unpin(1);
    pool.assertQuiescent();
  });
});
