import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, truncateSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { EngineError, ErrorCodes } from "../src/errors.ts";
import { MetricsRegistry } from "../src/metrics/registry.ts";
import { LogType } from "../src/wal/record.ts";
import { scanWal, WalWriter } from "../src/wal/wal.ts";

function setup() {
  const dir = mkdtempSync(join(tmpdir(), "wal-test-"));
  const metrics = new MetricsRegistry();
  return { path: join(dir, "wal.log"), metrics };
}

describe("WalWriter/scanWal", () => {
  it("round-trips records with sequential LSNs", () => {
    const { path, metrics } = setup();
    const wal = new WalWriter(path, metrics);
    const r1 = wal.append({ prevLSN: 0, txnId: 1, type: LogType.UPDATE, pageId: 0, beforeImage: "a", afterImage: "b" });
    const r2 = wal.append({ prevLSN: r1.lsn, txnId: 1, type: LogType.COMMIT });
    wal.close();
    assert.equal(r1.lsn, 1);
    assert.equal(r2.lsn, 2);

    const scan = scanWal(path, metrics);
    assert.equal(scan.tornTail, false);
    assert.equal(scan.records.length, 2);
    assert.deepEqual(scan.records[0], r1);
    assert.deepEqual(scan.records[1], r2);
  });

  it("rejects appends after close with E_WAL_IO", () => {
    const { path, metrics } = setup();
    const wal = new WalWriter(path, metrics);
    wal.close();
    assert.equal(wal.closed, true);
    assert.throws(
      () => wal.append({ prevLSN: 0, txnId: 1, type: LogType.END }),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.WAL_IO,
    );
  });

  it("close is idempotent", () => {
    const { path, metrics } = setup();
    const wal = new WalWriter(path, metrics);
    wal.close();
    wal.close();
    assert.equal(wal.closed, true);
  });

  it("throws when scanning a missing WAL file", () => {
    const { metrics } = setup();
    assert.throws(
      () => scanWal("/nonexistent/wal.log", metrics),
      (e: unknown) => e instanceof EngineError && e.code === ErrorCodes.WAL_IO,
    );
  });

  it("detects a truncated tail frame as torn (crash point)", () => {
    const { path, metrics } = setup();
    const wal = new WalWriter(path, metrics);
    wal.append({ prevLSN: 0, txnId: 1, type: LogType.UPDATE, pageId: 0, beforeImage: "a", afterImage: "b" });
    wal.append({ prevLSN: 1, txnId: 1, type: LogType.UPDATE, pageId: 1, beforeImage: "c", afterImage: "d" });
    wal.close();
    const size = readFileSync(path).length;
    truncateSync(path, size - 3); // simulate torn write at crash
    const scan = scanWal(path, metrics);
    assert.equal(scan.tornTail, true);
    assert.equal(scan.records.length, 1);
    assert.equal(metrics.counter("wal.torn_tail").get(), 1);
  });

  it("detects checksum mismatch and records the error code", () => {
    const { path, metrics } = setup();
    const wal = new WalWriter(path, metrics);
    wal.append({ prevLSN: 0, txnId: 1, type: LogType.UPDATE, pageId: 0, beforeImage: "a", afterImage: "b" });
    wal.close();
    const buf = readFileSync(path);
    buf[buf.length - 1] = buf[buf.length - 1]! ^ 0xff; // corrupt payload
    writeFileSync(path, buf);
    const scan = scanWal(path, metrics);
    assert.equal(scan.tornTail, true);
    assert.equal(scan.records.length, 0);
    assert.equal(metrics.counter("errors.total", { code: ErrorCodes.WAL_CHECKSUM_MISMATCH }).get(), 1);
  });

  it("append mode continues the LSN sequence without truncating", () => {
    const { path, metrics } = setup();
    const wal = new WalWriter(path, metrics);
    wal.append({ prevLSN: 0, txnId: 1, type: LogType.UPDATE, pageId: 0, beforeImage: "a", afterImage: "b" });
    wal.close();
    const appender = WalWriter.append(path, metrics, 2);
    const clr = appender.append({ prevLSN: 1, txnId: 1, type: LogType.CLR, pageId: 0, undoNextLSN: 0, afterImage: "a" });
    appender.close();
    assert.equal(clr.lsn, 2);
    const scan = scanWal(path, metrics);
    assert.equal(scan.records.length, 2);
    assert.equal(scan.records[1]!.type, LogType.CLR);
  });
});
