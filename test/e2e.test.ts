/**
 * 端到端验收：3 个未提交事务 + 2 个已提交事务，检查点之后崩溃。
 * 恢复引擎执行 Analysis/Redo/Undo 三阶段扫描，
 * 准确回滚未提交修改且持久化已提交数据，断言严格无泄漏。
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { RecoveryEngine } from '../src/recovery.ts';
import { readWal } from '../src/wal.ts';
import { LogType } from '../src/log-record.ts';
import { DiskStorage } from '../src/storage.ts';
import { buildCrashScenario } from '../examples/scenario.ts';

test('验收：三阶段恢复后已提交持久、未提交回滚、状态无泄漏', () => {
  const dir = mkdtempSync(join(tmpdir(), 'aries-e2e-'));
  try {
    const scenario = buildCrashScenario(dir);
    assert.deepEqual(scenario.txns.committed, [1, 2]);
    assert.deepEqual(scenario.txns.uncommitted, [3, 4, 5]);

    // 崩溃现场：未提交数据已 steal 落盘，已提交数据部分丢失（P2 仅在缓冲区）。
    const atCrash = new DiskStorage(scenario.storagePath).snapshot();
    assert.equal(atCrash.P1.value, 11, '未提交的 11 已落盘（steal）');
    assert.equal(atCrash.P2, undefined, '已提交的 P2=20 尚未落盘（no-force）');

    const engine = new RecoveryEngine(scenario.walPath, scenario.storagePath);
    const report = engine.recover();

    // Analysis：恰好识别 3 个 loser。
    assert.deepEqual([...report.analysis.txnTable.keys()].sort(), [3, 4, 5]);

    // 最终磁盘：精确等于已提交状态。
    const finalDisk = new DiskStorage(scenario.storagePath).snapshot();
    for (const [pageId, expected] of Object.entries(scenario.expected)) {
      assert.equal(finalDisk[pageId]?.value, expected, `page ${pageId}`);
    }

    // 无泄漏断言：
    assert.deepEqual(report.openTransactions, [], '恢复后事务表必须为空');
    assert.equal(engine.isClosed, true, '恢复引擎必须关闭');
    const wal = readWal(scenario.walPath);
    const openTxns = new Set<number>();
    for (const r of wal) {
      if (r.txnId === null) continue;
      if (r.type === LogType.Update || r.type === LogType.Clr) openTxns.add(r.txnId);
      if (r.type === LogType.End) openTxns.delete(r.txnId);
    }
    assert.deepEqual([...openTxns], [], 'WAL 中每个事务都有 END 终结（无悬挂事务）');

    // 每个 loser 的每条 UPDATE 都恰好被一条 CLR 补偿（无遗漏、无重复）。
    const updatesByTxn = new Map<number, number>();
    const clrsByTxn = new Map<number, number>();
    for (const r of wal) {
      if (r.type === LogType.Update) updatesByTxn.set(r.txnId as number, (updatesByTxn.get(r.txnId as number) ?? 0) + 1);
      if (r.type === LogType.Clr) clrsByTxn.set(r.txnId as number, (clrsByTxn.get(r.txnId as number) ?? 0) + 1);
    }
    for (const loser of scenario.txns.uncommitted) {
      assert.equal(clrsByTxn.get(loser) ?? 0, updatesByTxn.get(loser) ?? 0, `txn ${loser} 补偿完整`);
    }
    for (const winner of scenario.txns.committed) {
      assert.equal(clrsByTxn.get(winner) ?? 0, 0, `txn ${winner} 已提交，绝不被补偿`);
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('验收：崩溃-恢复-再崩溃-再恢复，结果收敛到同一状态', () => {
  const dir = mkdtempSync(join(tmpdir(), 'aries-e2e-'));
  try {
    const scenario = buildCrashScenario(dir);
    // 第一次恢复到一半崩溃（只写 1 条 CLR）。
    new RecoveryEngine(scenario.walPath, scenario.storagePath).recover({ maxUndoClrs: 1 });
    // 第二次恢复到一半又崩溃（再写 2 条 CLR）。
    new RecoveryEngine(scenario.walPath, scenario.storagePath).recover({ maxUndoClrs: 2 });
    // 第三次完整恢复。
    const report = new RecoveryEngine(scenario.walPath, scenario.storagePath).recover();

    const finalDisk = new DiskStorage(scenario.storagePath).snapshot();
    for (const [pageId, expected] of Object.entries(scenario.expected)) {
      assert.equal(finalDisk[pageId]?.value, expected, `page ${pageId}`);
    }
    assert.deepEqual(report.openTransactions, []);

    // 全程 CLR 总数 = loser 的 UPDATE 总数（5），一次不多一次不少。
    const totalClrs = readWal(scenario.walPath).filter((r) => r.type === LogType.Clr).length;
    assert.equal(totalClrs, 5);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
