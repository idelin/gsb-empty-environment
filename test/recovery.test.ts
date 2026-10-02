import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { AriesRecovery } from "../src/recovery.ts";
import { buildCrashScenario } from "../src/scenario.ts";
import { DiskManager } from "../src/storage.ts";
import { LogType, TxnStatus } from "../src/types.ts";
import { WalManager } from "../src/wal.ts";

function freshDir(): string {
  return mkdtempSync(join(tmpdir(), "aries-test-"));
}

test("analysis rebuilds transaction table and dirty page table from checkpoint", () => {
  const dir = freshDir();
  const scenario = buildCrashScenario(dir);
  const recovery = new AriesRecovery(
    WalManager.open(scenario.walPath),
    DiskManager.open(scenario.diskPath),
  );
  const { transactionTable, dirtyPageTable, redoStartLSN } = recovery.analysis();

  assert.equal(transactionTable.size, 5);
  assert.equal(transactionTable.get(1)!.status, TxnStatus.COMMITTED);
  assert.equal(transactionTable.get(2)!.status, TxnStatus.COMMITTED);
  assert.equal(transactionTable.get(3)!.status, TxnStatus.RUNNING);
  assert.equal(transactionTable.get(4)!.status, TxnStatus.RUNNING);
  assert.equal(transactionTable.get(5)!.status, TxnStatus.RUNNING);
  assert.equal(transactionTable.get(1)!.lastLSN, 9); // COMMIT T1
  assert.equal(transactionTable.get(2)!.lastLSN, 13); // COMMIT T2
  assert.equal(transactionTable.get(3)!.lastLSN, 6);
  assert.equal(transactionTable.get(4)!.lastLSN, 11);
  assert.equal(transactionTable.get(5)!.lastLSN, 15);

  assert.equal(dirtyPageTable.get("P1")!.recLSN, 2); // seeded from checkpoint
  assert.equal(dirtyPageTable.get("P2")!.recLSN, 4); // seeded from checkpoint
  assert.equal(dirtyPageTable.get("P3")!.recLSN, 8);
  assert.equal(dirtyPageTable.get("P4")!.recLSN, 12);
  assert.equal(dirtyPageTable.get("P5")!.recLSN, 15);
  assert.equal(redoStartLSN, 2); // min recLSN
});

test("full recovery: redo repeats history, undo rolls back losers with CLRs", () => {
  const dir = freshDir();
  const scenario = buildCrashScenario(dir);
  const wal = WalManager.open(scenario.walPath);
  const disk = DiskManager.open(scenario.diskPath);
  const recovery = new AriesRecovery(wal, disk);
  const report = recovery.recover();

  // REDO repeated exactly the updates that were missing from disk:
  // LSN 8 (T1 committed P3, never flushed) and LSN 15 (T5 loser P5).
  assert.deepEqual(report.redoneLSNs, [8, 15]);

  // UNDO processed losers in reverse LSN order: T5(15), T4(11), T3(6).
  assert.deepEqual(report.undoneLSNs, [15, 11, 6]);
  assert.equal(report.clrLSNs.length, 3);

  // Committed data persisted, uncommitted data rolled back.
  const pages = new Map(disk.allPages().map((p) => [p.pageId, p.data]));
  for (const [pageId, expected] of Object.entries(scenario.expectedAfterRecovery)) {
    assert.equal(pages.get(pageId), expected, `page ${pageId}`);
  }

  // CLRs carry undoNextLSN pointing at the undone record's prevLSN.
  const clrs = wal.all().filter((r) => r.type === LogType.CLR);
  assert.equal(clrs.length, 3);
  const clrByTxn = new Map(clrs.map((c) => [c.txnId, c]));
  assert.equal(clrByTxn.get(5)!.undoNextLSN, 14); // -> BEGIN T5
  assert.equal(clrByTxn.get(4)!.undoNextLSN, 10); // -> BEGIN T4
  assert.equal(clrByTxn.get(3)!.undoNextLSN, 5); // -> BEGIN T3
  // CLR after-image is the original before-image (compensation).
  assert.equal(clrByTxn.get(3)!.afterImage, "A1");
  assert.equal(clrByTxn.get(4)!.afterImage, "B1");
  assert.equal(clrByTxn.get(5)!.afterImage, "E");

  // No leaks: every loser transaction is terminated with an END record.
  const ends = wal.all().filter((r) => r.type === LogType.END).map((r) => r.txnId);
  assert.deepEqual(ends.sort(), [3, 4, 5]);

  // Every loser UPDATE is compensated by exactly one CLR.
  const loserUpdates = wal
    .all()
    .filter((r) => r.type === LogType.UPDATE && [3, 4, 5].includes(r.txnId));
  assert.equal(clrs.length, loserUpdates.length);
});

test("redo is idempotent: a second pass applies nothing", () => {
  const dir = freshDir();
  const scenario = buildCrashScenario(dir);
  const wal = WalManager.open(scenario.walPath);
  const disk = DiskManager.open(scenario.diskPath);
  const recovery = new AriesRecovery(wal, disk);

  const analysis = recovery.analysis();
  const first = recovery.redo(analysis);
  const second = recovery.redo(analysis);
  assert.deepEqual(first, [8, 15]);
  assert.deepEqual(second, []);
});

test("crash during undo: existing CLRs are skipped, not undone twice", () => {
  const dir = freshDir();
  const scenario = buildCrashScenario(dir);
  const wal = WalManager.open(scenario.walPath);
  const disk = DiskManager.open(scenario.diskPath);

  // Simulate a first recovery attempt that undid T5 (CLR) then crashed.
  const clr = wal.append({
    type: LogType.CLR,
    txnId: 5,
    prevLSN: 15,
    pageId: "P5",
    beforeImage: "E1",
    afterImage: "E",
    undoNextLSN: 14,
  });
  disk.writePage("P5", "E", clr.lsn);

  const recovery = new AriesRecovery(wal, disk);
  const report = recovery.recover();

  // LSN 15 must NOT be undone again: only T4(11) and T3(6) remain.
  assert.deepEqual(report.undoneLSNs, [11, 6]);
  assert.equal(report.clrLSNs.length, 2);
  const pages = new Map(disk.allPages().map((p) => [p.pageId, p.data]));
  assert.equal(pages.get("P5"), "E");
  assert.equal(pages.get("P1"), "A1");
  assert.equal(pages.get("P2"), "B1");
});

test("analysis works without any checkpoint (full log scan)", () => {
  const dir = freshDir();
  const wal = WalManager.create(join(dir, "wal.log"));
  DiskManager.create(join(dir, "disk.json"));

  wal.append({ type: LogType.BEGIN, txnId: 1, prevLSN: 0 });
  wal.append({
    type: LogType.UPDATE, txnId: 1, prevLSN: 1,
    pageId: "P1", beforeImage: "x", afterImage: "y",
  });
  wal.append({ type: LogType.BEGIN, txnId: 2, prevLSN: 0 });
  wal.append({ type: LogType.COMMIT, txnId: 2, prevLSN: 3 });
  wal.append({ type: LogType.END, txnId: 2, prevLSN: 4 });

  const recovery = new AriesRecovery(wal, DiskManager.open(join(dir, "disk.json")));
  const { transactionTable, dirtyPageTable, redoStartLSN } = recovery.analysis();

  assert.equal(transactionTable.size, 1); // T2 ended and was removed
  assert.equal(transactionTable.get(1)!.status, TxnStatus.RUNNING);
  assert.equal(dirtyPageTable.get("P1")!.recLSN, 2);
  assert.equal(redoStartLSN, 2);
});

test("redo skips pages already up to date on disk", () => {
  const dir = freshDir();
  const wal = WalManager.create(join(dir, "wal.log"));
  const disk = DiskManager.create(join(dir, "disk.json"));

  wal.append({ type: LogType.BEGIN, txnId: 1, prevLSN: 0 });
  wal.append({
    type: LogType.UPDATE, txnId: 1, prevLSN: 1,
    pageId: "P1", beforeImage: "a", afterImage: "b",
  });
  // Page on disk is already newer than the logged update.
  disk.writePage("P1", "b", 2);

  const recovery = new AriesRecovery(wal, disk);
  const analysis = recovery.analysis();
  assert.deepEqual(recovery.redo(analysis), []); // pageLSN 2 >= lsn 2
  assert.equal(disk.readPage("P1").data, "b");
});
