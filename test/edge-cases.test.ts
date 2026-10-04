import { test } from "node:test";
import assert from "node:assert/strict";
import { join } from "node:path";
import { LogRecordType, NULL_LSN, TxnStatus } from "../src/log-record.ts";
import { attach, makeTempDir, pageLSN, pageValue, writeWalFile } from "./helpers.ts";

test("边界: 空 WAL 恢复为无操作", async () => {
  const dir = makeTempDir();
  const h = attach(dir);
  const report = await h.engine.recover();
  assert.equal(report.analysis.txnTable.length, 0);
  assert.equal(report.redo.applied, 0);
  assert.equal(report.undo.clrWritten, 0);
  assert.equal(report.undo.endWritten, 0);
});

test("边界: 仅含 checkpoint 的 WAL", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.CHECKPOINT, txnId: -1, prevLSN: NULL_LSN, txnTable: [], dirtyPageTable: [] },
  ]);
  const h = attach(dir);
  const report = await h.engine.recover();
  assert.equal(report.analysis.txnTable.length, 0);
  assert.equal(report.undo.loserTxns.length, 0);
});

test("边界: 单条更新的 loser, CLR 的 undoNextLSN 指回 BEGIN", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN }, // lsn 1
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 5 }, // lsn 2
  ]);
  const h = attach(dir);
  const report = await h.engine.recover();
  assert.deepEqual(report.undo.loserTxns, [1]);
  assert.equal(report.undo.clrWritten, 1);
  assert.equal(report.undo.endWritten, 1);

  const clr = h.wal.allRecords().find((r) => r.type === LogRecordType.CLR);
  assert.ok(clr && clr.type === LogRecordType.CLR);
  assert.equal(clr.undoNextLSN, 1, "undoNextLSN -> BEGIN record");
  assert.equal(clr.after, 0, "CLR restores before-image");
  assert.equal(pageValue(h.disk, "P1"), 0);
});

test("边界: 已提交但未写 END 的事务是 winner, 不被回滚", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN },
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 7 },
    { type: LogRecordType.COMMIT, txnId: 1, prevLSN: 2 }, // 崩溃在 END 之前
  ]);
  const h = attach(dir);
  const report = await h.engine.recover();
  assert.deepEqual(report.undo.winnerTxns, [1]);
  assert.deepEqual(report.undo.loserTxns, []);
  assert.equal(report.undo.clrWritten, 0);
  assert.equal(pageValue(h.disk, "P1"), 7, "committed update survives");
});

test("边界: 崩溃时 ABORTING 且补偿已完成的 loser 只补 END, 不写新 CLR", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN }, // 1
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 9 }, // 2
    { type: LogRecordType.ABORT, txnId: 1, prevLSN: 2 }, // 3
    // 4: CLR 已补偿唯一更新, undoNextLSN=1 (BEGIN)
    { type: LogRecordType.CLR, txnId: 1, prevLSN: 3, pageId: "P1", before: 9, after: 0, undoNextLSN: 1 },
  ]);
  const h = attach(dir);
  const report = await h.engine.recover();
  assert.equal(report.undo.clrWritten, 0, "no double compensation");
  assert.equal(report.undo.endWritten, 1, "abort finalized with END");
  assert.equal(pageValue(h.disk, "P1"), 0);
});

test("边界: undoNextLSN=0 的 CLR 直接终止该事务的 undo 链", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN }, // 1
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 3 }, // 2
    { type: LogRecordType.ABORT, txnId: 1, prevLSN: 2 }, // 3
    // undoNextLSN=0: 表示补偿已覆盖到事务起点 (越过 BEGIN)
    { type: LogRecordType.CLR, txnId: 1, prevLSN: 3, pageId: "P1", before: 3, after: 0, undoNextLSN: NULL_LSN }, // 4
  ]);
  const h = attach(dir);
  const report = await h.engine.recover();
  assert.equal(report.undo.clrWritten, 0);
  assert.equal(report.undo.endWritten, 1);
  assert.equal(h.engine.analysis(h.wal.allRecords()).txnTable.size, 0);
});

test("边界: 多 loser 按全局 LSN 逆序回滚 (大 LSN 优先)", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN }, // 1
    { type: LogRecordType.BEGIN, txnId: 2, prevLSN: NULL_LSN }, // 2
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 1 }, // 3
    { type: LogRecordType.UPDATE, txnId: 2, prevLSN: 2, pageId: "P2", before: 0, after: 2 }, // 4
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 3, pageId: "P3", before: 0, after: 3 }, // 5
  ]);
  const h = attach(dir);
  await h.engine.recover();

  const clrs = h.wal.allRecords().filter((r) => r.type === LogRecordType.CLR);
  assert.equal(clrs.length, 3);
  // 逆序: lsn5(P3) -> lsn4(P2) -> lsn3(P1)
  assert.deepEqual(
    clrs.map((c) => (c.type === LogRecordType.CLR ? c.pageId : "")),
    ["P3", "P2", "P1"],
  );
  assert.equal(pageValue(h.disk, "P1"), 0);
  assert.equal(pageValue(h.disk, "P2"), 0);
  assert.equal(pageValue(h.disk, "P3"), 0);
});

test("边界: Redo 尊重 pageLSN — 磁盘页已更新时跳过", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN },
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 42 },
    { type: LogRecordType.COMMIT, txnId: 1, prevLSN: 2 },
    { type: LogRecordType.END, txnId: 1, prevLSN: 3 },
  ]);
  // 磁盘页已包含该更新 (pageLSN=2 >= 记录 LSN)
  const h0 = attach(dir);
  h0.disk.writePage("P1", 42, 2);

  const h = attach(dir);
  const report = await h.engine.recover();
  assert.equal(report.redo.applied, 0, "page already reflects the update");
  assert.equal(report.redo.skipped, 1);
  assert.equal(pageValue(h.disk, "P1"), 42);
  assert.equal(pageLSN(h.disk, "P1"), 2);
});

test("边界: Redo 从 DPT 最小 recLSN 开始, 之前的记录不扫描", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN }, // 1
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 1 }, // 2
    { type: LogRecordType.COMMIT, txnId: 1, prevLSN: 2 }, // 3
    { type: LogRecordType.END, txnId: 1, prevLSN: 3 }, // 4
    // checkpoint: P1 已刷盘 (不在 DPT), 仅 P2 脏, recLSN=7
    {
      type: LogRecordType.CHECKPOINT, txnId: -1, prevLSN: NULL_LSN,
      txnTable: [{ txnId: 2, status: TxnStatus.RUNNING, lastLSN: 6 }],
      dirtyPageTable: [{ pageId: "P2", recLSN: 7 }],
    }, // 5
    { type: LogRecordType.BEGIN, txnId: 2, prevLSN: NULL_LSN }, // 6 (checkpoint 前已开始)
    { type: LogRecordType.UPDATE, txnId: 2, prevLSN: 6, pageId: "P2", before: 0, after: 2 }, // 7
  ]);
  const h = attach(dir);
  const report = await h.engine.recover();
  assert.equal(report.redo.fromLSN, 7, "starts at min recLSN, not log start");
  assert.equal(pageValue(h.disk, "P2"), 0, "loser update undone");
});

test("边界: checkpoint 中的活跃事务快照与后续日志正确合并", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN }, // 1
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 1 }, // 2
    {
      type: LogRecordType.CHECKPOINT, txnId: -1, prevLSN: NULL_LSN,
      txnTable: [{ txnId: 1, status: TxnStatus.RUNNING, lastLSN: 2 }],
      dirtyPageTable: [{ pageId: "P1", recLSN: 2 }],
    }, // 3
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 2, pageId: "P2", before: 0, after: 2 }, // 4
    { type: LogRecordType.COMMIT, txnId: 1, prevLSN: 4 }, // 5
  ]);
  const h = attach(dir);
  const report = await h.engine.recover();
  const entry = report.analysis.txnTable.find((t) => t.txnId === 1);
  assert.equal(entry?.status, TxnStatus.COMMITTED, "checkpoint 快照状态被后续 COMMIT 推进");
  assert.equal(entry?.lastLSN, 5);
  assert.deepEqual(
    report.analysis.dirtyPageTable.map((d) => d.pageId).sort(),
    ["P1", "P2"],
  );
  assert.deepEqual(report.undo.winnerTxns, [1]);
  assert.equal(pageValue(h.disk, "P1"), 1);
  assert.equal(pageValue(h.disk, "P2"), 2);
});

test("边界: 恢复期间崩溃前的 CLR 本身被 Redo 重复 (repeat history)", async () => {
  const dir = makeTempDir();
  writeWalFile(join(dir, "wal.log"), [
    { type: LogRecordType.BEGIN, txnId: 1, prevLSN: NULL_LSN }, // 1
    { type: LogRecordType.UPDATE, txnId: 1, prevLSN: 1, pageId: "P1", before: 0, after: 8 }, // 2
    { type: LogRecordType.ABORT, txnId: 1, prevLSN: 2 }, // 3
    { type: LogRecordType.CLR, txnId: 1, prevLSN: 3, pageId: "P1", before: 8, after: 0, undoNextLSN: 1 }, // 4
  ]);
  // 磁盘停留在 UPDATE 之后、CLR 之前的状态 (pageLSN=2, 值=8)
  const h0 = attach(dir);
  h0.disk.writePage("P1", 8, 2);

  const h = attach(dir);
  const report = await h.engine.recover();
  // Redo 必须重放 CLR (lsn4 > pageLSN 2), 将 P1 恢复为 0
  assert.ok(report.redo.applied >= 1);
  assert.equal(pageValue(h.disk, "P1"), 0);
  assert.equal(pageLSN(h.disk, "P1"), 4, "pageLSN advanced by CLR redo");
  assert.equal(report.undo.clrWritten, 0, "undo skips compensated update");
});
