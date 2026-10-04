import { test } from "node:test";
import assert from "node:assert/strict";
import { attachRecovery, buildCrashScenario } from "../src/scenario.ts";
import { LogRecordType, TxnStatus } from "../src/log-record.ts";
import { makeTempDir, pageValue } from "./helpers.ts";
import { DiskStorage } from "../src/storage.ts";

test("验收场景: 3个未提交 + 2个已提交事务, checkpoint后崩溃, 三阶段恢复", async () => {
  const dir = makeTempDir();
  const scenario = await buildCrashScenario(dir);
  const { engine, wal } = await attachRecovery(scenario);

  const report = await engine.recover();

  // Analysis: 崩溃时恰好 3 个活跃事务进入事务表
  const table = new Map(report.analysis.txnTable.map((t) => [t.txnId, t]));
  assert.equal(table.size, 3);
  const [l3, l4, l5] = [
    scenario.txnIds.uncommitted[0]!,
    scenario.txnIds.uncommitted[1]!,
    scenario.txnIds.abortingAtCrash,
  ];
  assert.equal(table.get(l3)?.status, TxnStatus.RUNNING);
  assert.equal(table.get(l4)?.status, TxnStatus.RUNNING);
  assert.equal(table.get(l5)?.status, TxnStatus.ABORTING);
  // 已提交事务已写 END, 不在事务表中
  for (const winner of scenario.txnIds.committed) {
    assert.equal(table.has(winner), false);
  }
  // DPT 覆盖全部 4 个被修改页
  assert.deepEqual(
    report.analysis.dirtyPageTable.map((d) => d.pageId).sort(),
    ["P1", "P2", "P3", "P4"],
  );

  // Redo: 从最小 recLS 开始历史重复
  const minRec = Math.min(...report.analysis.dirtyPageTable.map((d) => d.recLSN));
  assert.equal(report.redo.fromLSN, minRec);
  assert.ok(report.redo.applied > 0);

  // Undo: 3 个 loser 全部回滚; T3 两次更新 + T4 一次更新 = 3 个 CLR
  // (T5 的更新崩溃前已被 CLR 补偿, undoNextLSN 跳过, 不重复补偿)
  assert.deepEqual(report.undo.loserTxns.sort(), [l3, l4, l5].sort());
  assert.equal(report.undo.clrWritten, 3);
  assert.equal(report.undo.endWritten, 3);

  // 最终一致性: 已提交数据持久化, 未提交数据回滚
  const disk = new DiskStorage(scenario.dbPath);
  for (const [pageId, value] of Object.entries(scenario.expected)) {
    assert.equal(pageValue(disk, pageId), value, `page ${pageId}`);
  }

  // 每个 loser 都有 END 记录收尾, 无泄漏事务
  const finalLog = wal.allRecords();
  for (const loser of [l3, l4, l5]) {
    assert.ok(
      finalLog.some((r) => r.type === LogRecordType.END && r.txnId === loser),
      `loser T${loser} must be finalized with END`,
    );
  }
  // 恢复后日志再做一次 Analysis: 事务表必须为空
  const after = engine.analysis(finalLog);
  assert.equal(after.txnTable.size, 0, "no leaked transactions after recovery");
});

test("CLR 补偿链: undoNextLSN 正确链接且崩溃前的 CLR 不被重复补偿", async () => {
  const dir = makeTempDir();
  const scenario = await buildCrashScenario(dir);
  const { engine, wal } = await attachRecovery(scenario);
  await engine.recover();

  const log = wal.allRecords();
  const byLSN = new Map(log.map((r) => [r.lsn, r]));
  const clrs = log.filter((r) => r.type === LogRecordType.CLR);
  // 崩溃前 T5 已有 1 个 CLR, 恢复新增 3 个, 共 4 个
  assert.equal(clrs.length, 4);

  for (const clr of clrs) {
    assert.equal(clr.type, LogRecordType.CLR);
    // undoNextLSN 必须指向被补偿 UPDATE 的 prevLSN (即同一事务的下一条待撤销记录)
    const target = byLSN.get(clr.undoNextLSN);
    assert.ok(target, `CLR@${clr.lsn} undoNextLSN must resolve`);
    assert.equal(target.txnId, clr.txnId);
  }

  // T5 (崩溃时 ABORTING): 恢复期间不得为其写新 CLR —— 其更新已被补偿
  const t5 = scenario.txnIds.abortingAtCrash;
  const t5PreCrashClrs = 1;
  const t5Clrs = clrs.filter((r) => r.txnId === t5);
  assert.equal(t5Clrs.length, t5PreCrashClrs, "no double compensation for T5");
});

test("Redo 幂等: 重复执行不产生额外应用, pageLSN 不变", async () => {
  const dir = makeTempDir();
  const scenario = await buildCrashScenario(dir);
  const { engine, pool, wal } = await attachRecovery(scenario);

  const log = wal.allRecords();
  const analysis = engine.analysis(log);
  const first = engine.redo(log, analysis);
  const lsnAfterFirst = ["P1", "P2", "P3", "P4"].map(
    (p) => pool.fetchPage(p).pageLSN,
  );
  const second = engine.redo(log, analysis);
  const lsnAfterSecond = ["P1", "P2", "P3", "P4"].map(
    (p) => pool.fetchPage(p).pageLSN,
  );

  assert.ok(first.applied > 0);
  assert.equal(second.applied, 0, "second redo must apply nothing");
  assert.deepEqual(lsnAfterSecond, lsnAfterFirst);
});

test("完整恢复幂等: 第二次 recover() 完全无操作且无新增日志", async () => {
  const dir = makeTempDir();
  const scenario = await buildCrashScenario(dir);
  const { engine, wal } = await attachRecovery(scenario);

  await engine.recover();
  const diskImage1 = JSON.stringify(
    ["P1", "P2", "P3", "P4"].map((p) => new DiskStorage(scenario.dbPath).readPage(p)),
  );
  const logLen1 = wal.allRecords().length;

  const report2 = await engine.recover();
  assert.equal(report2.redo.applied, 0);
  assert.equal(report2.undo.clrWritten, 0);
  assert.equal(report2.undo.endWritten, 0);
  assert.equal(report2.undo.loserTxns.length, 0);
  assert.equal(wal.allRecords().length, logLen1, "no new log records");

  const diskImage2 = JSON.stringify(
    ["P1", "P2", "P3", "P4"].map((p) => new DiskStorage(scenario.dbPath).readPage(p)),
  );
  assert.equal(diskImage2, diskImage1);
});

test("无泄漏断言: 恢复后锁释放 / 缓冲池无脏页 / 事务表为空", async () => {
  const dir = makeTempDir();
  const scenario = await buildCrashScenario(dir);
  const { engine, pool, wal } = await attachRecovery(scenario);
  await engine.recover();

  assert.equal(engine.lock.isLocked, false, "recovery mutex released");
  assert.equal(engine.lock.pending, 0, "no queued waiters");
  assert.equal(wal.appendLock.isLocked, false, "wal append mutex released");
  assert.equal(pool.dirtyPageIds().length, 0, "all dirty pages flushed");
  assert.equal(engine.analysis(wal.allRecords()).txnTable.size, 0);
});
