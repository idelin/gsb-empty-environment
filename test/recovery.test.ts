import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ErrorCodes } from "../src/errors.ts";
import { MetricsRegistry } from "../src/metrics/registry.ts";
import { RecoveryEngine } from "../src/recovery/aries.ts";
import { BufferPool } from "../src/storage/bufferPool.ts";
import { DiskManager } from "../src/storage/disk.ts";
import { LogType } from "../src/wal/record.ts";
import { scanWal, WalWriter } from "../src/wal/wal.ts";
import { TransactionManager } from "../src/txn/transactionManager.ts";
import { buildCrashScenario, EXPECTED_DATA, PAGE_IDS } from "../examples/scenario.ts";

function recover(site: { walPath: string; diskPath: string }) {
  const metrics = new MetricsRegistry();
  const disk = new DiskManager(site.diskPath, 4096, 16, metrics);
  const pool = new BufferPool(disk, metrics);
  const engine = new RecoveryEngine(site.walPath, disk, pool, metrics);
  const report = engine.recover();
  return { metrics, disk, pool, report };
}

describe("ARIES recovery (acceptance scenario)", () => {
  it("rolls back 3 uncommitted txns and persists 2 committed ones", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const site = buildCrashScenario(dir);
    const { disk, report } = recover(site);

    // losers: T3, T4, T5 (T1, T2 committed after the checkpoint)
    assert.deepEqual(report.losers, [3, 4, 5]);
    assert.equal(report.undoCLRs, 4); // T3 wrote 2 updates, T4/T5 one each

    for (const [name, pageId] of Object.entries(PAGE_IDS)) {
      const page = disk.readPage(pageId);
      assert.equal(page.data, EXPECTED_DATA[pageId], `page ${name} must hold committed state`);
    }
    disk.close();
  });

  it("analysis rebuilds DPT and TT exactly as of the crash", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const site = buildCrashScenario(dir);
    const { report } = recover(site);

    // DPT: checkpoint snapshot {P1:1,P2:2,P3:3} plus P4 (LSN 9) and P5 (LSN 12)
    assert.deepEqual(
      [...report.dirtyPageTable.entries()].sort((a, b) => a[0] - b[0]),
      [[PAGE_IDS.P1, 1], [PAGE_IDS.P2, 2], [PAGE_IDS.P3, 3], [PAGE_IDS.P4, 9], [PAGE_IDS.P5, 12]],
    );
    // TT at crash: only the three losers remain (END removed T1/T2)
    assert.deepEqual([...report.transactionTable.keys()].sort(), [3, 4, 5]);
    for (const entry of report.transactionTable.values()) {
      assert.equal(entry.status, "RUNNING");
    }
    assert.equal(report.transactionTable.get(3)!.lastLSN, 14);
    assert.equal(report.transactionTable.get(4)!.lastLSN, 12);
    assert.equal(report.transactionTable.get(5)!.lastLSN, 13);
  });

  it("redo repeats history from min recLSN and skips already-persisted records", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const site = buildCrashScenario(dir);
    const { report } = recover(site);
    assert.equal(report.redoStartLSN, 1);
    // applied: LSN 3 (P3), 12 (P5), 13 (P1 by T5), 14 (P3 by T3)
    assert.equal(report.redoApplied, 4);
    // skipped: LSN 1, 6 (P1 flushed at pageLSN 6), 2 (P2 flushed), 9 (P4 flushed)
    assert.equal(report.redoSkipped, 4);
  });

  it("undo writes CLRs with undoNextLSN chains and END records", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const site = buildCrashScenario(dir);
    const metrics = new MetricsRegistry();
    recover(site).disk.close();
    const scan = scanWal(site.walPath, metrics);
    const tail = scan.records.filter((r) => r.lsn > site.lastLSN);

    const clrs = tail.filter((r) => r.type === LogType.CLR);
    assert.equal(clrs.length, 4);
    // every CLR restores the before-image of the record it compensates
    const byLSN = new Map(scan.records.map((r) => [r.lsn, r]));
    for (const clr of clrs) {
      const original = byLSN.get(clr.prevLSN);
      assert.ok(original, `CLR ${clr.lsn} must reference its update record`);
      assert.equal(clr.afterImage, original.beforeImage);
      assert.equal(clr.undoNextLSN, original.prevLSN);
    }
    // T3 has two updates: its second CLR chains to the first via undoNextLSN
    const t3clrs = clrs.filter((r) => r.txnId === 3);
    assert.equal(t3clrs.length, 2);
    // END records terminate every loser
    const ends = tail.filter((r) => r.type === LogType.END).map((r) => r.txnId).sort();
    assert.deepEqual(ends, [3, 4, 5]);
  });

  it("undo processes losers in strictly descending LSN order", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const site = buildCrashScenario(dir);
    recover(site).disk.close();
    const scan = scanWal(site.walPath, new MetricsRegistry());
    const clrTargets = scan.records
      .filter((r) => r.lsn > site.lastLSN && r.type === LogType.CLR)
      .map((r) => r.prevLSN);
    // undone update LSNs: 14 (T3), 13 (T5), 12 (T4), then 3 (T3 chain)
    assert.deepEqual(clrTargets, [14, 13, 12, 3]);
  });

  it("leaves no leaked state: quiescent pool, zeroed gauges, closed fds", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const site = buildCrashScenario(dir);
    const { metrics, pool, disk } = recover(site);
    pool.assertQuiescent(); // no pinned or dirty frames
    assert.equal(metrics.gauge("recovery.dirty_pages").get(), 0);
    assert.equal(metrics.gauge("recovery.active_txns").get(), 0);
    disk.close();
    assert.equal(disk.closed, true);
  });

  it("records phase latency histograms and scan counters", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const site = buildCrashScenario(dir);
    const { metrics } = recover(site);
    for (const phase of ["analysis", "redo", "undo"]) {
      const h = metrics.histogram(`recovery.${phase}.duration_ms`);
      assert.equal(h.count, 1, `${phase} timer must record exactly one sample`);
      assert.ok(h.percentile(50) >= 0);
    }
    assert.ok(metrics.counter("recovery.analysis.records_scanned").get() > 0);
    assert.ok(metrics.counter("recovery.redo.records_scanned").get() > 0);
    assert.ok(metrics.counter("recovery.undo.records_scanned").get() > 0);
  });

  it("a second recovery pass is a no-op (ARIES idempotent restart)", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const site = buildCrashScenario(dir);
    recover(site).disk.close();

    const metrics = new MetricsRegistry();
    const disk = new DiskManager(site.diskPath, 4096, 16, metrics);
    const pool = new BufferPool(disk, metrics);
    const report = new RecoveryEngine(site.walPath, disk, pool, metrics).recover();
    assert.deepEqual(report.losers, []); // every txn ended
    assert.equal(report.undoCLRs, 0);
    for (const [pageId, expected] of Object.entries(EXPECTED_DATA)) {
      assert.equal(disk.readPage(Number(pageId)).data, expected);
    }
    disk.close();
  });

  it("recovers correctly when the WAL has no checkpoint at all", () => {
    const dir = mkdtempSync(join(tmpdir(), "aries-test-"));
    const metrics0 = new MetricsRegistry();
    const diskPath = join(dir, "data.db");
    const walPath = join(dir, "wal.log");
    const disk = new DiskManager(diskPath, 4096, 16, metrics0);
    disk.writePage(0, { pageLSN: 0, data: "A" });
    const pool = new BufferPool(disk, metrics0);
    const wal = new WalWriter(walPath, metrics0);
    const tm = new TransactionManager(wal, pool, metrics0);
    const t1 = tm.begin();
    tm.update(t1, 0, "A", "T1a");
    tm.crash();
    disk.close();

    const metrics = new MetricsRegistry();
    const disk2 = new DiskManager(diskPath, 4096, 16, metrics);
    const report = new RecoveryEngine(walPath, disk2, new BufferPool(disk2, metrics), metrics).recover();
    assert.deepEqual(report.losers, [1]);
    assert.equal(report.redoStartLSN, 1);
    assert.equal(disk2.readPage(0).data, "A");
    disk2.close();
  });
});
