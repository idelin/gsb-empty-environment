import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DBSystem } from '../src/system.ts';
import { cleanup, crashAndReopen, recordsOf, tempDir, walRecords } from './helpers.ts';

/**
 * 验收场景：3 个未提交事务 + 2 个已提交事务，检查点之后崩溃。
 * 恢复引擎执行三阶段扫描，回滚未提交修改并持久化已提交数据。
 */
async function buildAcceptanceScenario(dir: string) {
  const db = DBSystem.open(dir, { bufferPoolCapacity: 3 });
  // 种子数据
  const t0 = db.tx.begin();
  await db.tx.update(t0, 'P1', 'A');
  await db.tx.update(t0, 'P2', 'B');
  await db.tx.update(t0, 'P3', 'C');
  await db.tx.commit(t0);
  db.flush();

  // 2 个将提交的事务
  const t1 = db.tx.begin();
  await db.tx.update(t1, 'P1', 'A1');
  await db.tx.update(t1, 'P2', 'B1');
  const t2 = db.tx.begin();
  await db.tx.update(t2, 'P3', 'C2');

  db.checkpoint(); // 检查点：t1、t2 活跃

  await db.tx.commit(t1);

  // 3 个将崩溃时未提交的事务
  const t3 = db.tx.begin();
  await db.tx.update(t3, 'P1', 'A3');
  await db.tx.update(t3, 'P4', 'D3');
  await db.tx.commit(t2);
  const t4 = db.tx.begin();
  await db.tx.update(t4, 'P2', 'B4');
  await db.tx.update(t4, 'P5', 'E4');
  const t5 = db.tx.begin();
  await db.tx.update(t5, 'P3', 'C5');
  await db.tx.update(t5, 'P6', 'F5');
  await db.tx.update(t3, 'P4', 'D3b');
  return { db, txs: { t1, t2, t3, t4, t5 } };
}

test('端到端：检查点后崩溃，三阶段恢复达成最终一致', async () => {
  const dir = tempDir();
  try {
    let { db, txs } = await buildAcceptanceScenario(dir);
    const { t3, t4, t5 } = txs;
    db = crashAndReopen(db, { bufferPoolCapacity: 3 });

    const report = db.recover();

    // Analysis：3 个 loser，无 winner（已提交事务的 END 均已落盘）
    assert.deepEqual([...report.losers].sort(), [t3, t4, t5]);
    assert.deepEqual(report.winners, []);
    assert.ok(report.analysis.checkpointLSN !== null, '必须使用检查点');

    // Redo：重放了崩溃时未落盘的记录（含 loser 的更新）
    assert.ok(report.redoneLSNs.length > 0);

    // Undo：3 个 loser 共 7 条 UPDATE，全部补偿
    assert.equal(report.undoCLRs.length, 7);

    // 最终一致性：已提交的持久化，未提交的回滚
    const expected: Record<string, string> = {
      P1: 'A1',
      P2: 'B1',
      P3: 'C2',
      P4: '',
      P5: '',
      P6: '',
    };
    for (const [p, want] of Object.entries(expected)) {
      assert.equal(db.readPersistedPage(p).data, want, `${p} 终值错误`);
    }

    // 严格无泄漏
    assert.equal(report.analysis.txTable.size, 0, 'Undo 后事务表必须为空');
    assert.equal(db.lockManager.heldCount, 0, '锁无泄漏');
    assert.equal(db.tx.dirtyPageTable.size, 0, '刷盘后脏页表无残留');
    const allRecs = walRecords(db);
    const txIds = new Set(
      allRecs.filter((r) => 'txId' in r).map((r) => (r as { txId: number }).txId),
    );
    for (const txId of txIds) {
      assert.equal(
        recordsOf(db, txId).at(-1)!.type,
        'END',
        `事务 ${txId} 必须以 END 收尾`,
      );
    }
    // 每条 loser 的 UPDATE 恰好被一条 CLR 补偿
    const updates = allRecs.filter((r) => r.type === 'UPDATE' && [t3, t4, t5].includes(r.txId));
    const clrs = allRecs.filter((r) => r.type === 'CLR');
    assert.equal(clrs.length, updates.length, '补偿不多不少');

    // 幂等：再次恢复无副作用
    const walSize = db.wal.lastLSN;
    db = crashAndReopen(db);
    const report2 = db.recover();
    assert.equal(report2.undoCLRs.length, 0);
    assert.equal(report2.redoneLSNs.length, 0);
    assert.equal(db.wal.lastLSN, walSize, '重复恢复不得追加日志');
    for (const [p, want] of Object.entries(expected)) {
      assert.equal(db.readPersistedPage(p).data, want, `${p} 二次恢复后终值错误`);
    }
  } finally {
    cleanup(dir);
  }
});

test('端到端：无检查点崩溃也能正确恢复', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    const t2 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'keep');
    await db.tx.commit(t1);
    await db.tx.update(t2, 'P1', 'discard');
    await db.tx.update(t2, 'P2', 'discard');
    db = crashAndReopen(db);

    const report = db.recover();
    assert.equal(report.analysis.checkpointLSN, null);
    assert.deepEqual(report.losers, [t2]);
    assert.equal(db.readPersistedPage('P1').data, 'keep');
    assert.equal(db.readPersistedPage('P2').data, '');
  } finally {
    cleanup(dir);
  }
});

test('端到端：崩溃发生在恢复 Undo 中途，再次恢复完成最终一致', async () => {
  const dir = tempDir();
  try {
    let { db } = await buildAcceptanceScenario(dir);
    db = crashAndReopen(db, { bufferPoolCapacity: 3 });

    // 第一次恢复：第 3 条 CLR 后崩溃
    let clrCount = 0;
    assert.throws(
      () =>
        db.recover({
          afterAppend(rec) {
            if (rec.type === 'CLR' && ++clrCount === 3) {
              throw new Error('injected crash during recovery undo');
            }
          },
        }),
      /injected crash during recovery undo/,
    );

    // 第二次恢复：完成剩余补偿
    db = crashAndReopen(db, { bufferPoolCapacity: 3 });
    const report = db.recover();
    assert.equal(report.undoCLRs.length, 4, '7 条补偿已完成 3 条，剩 4 条');
    const expected: Record<string, string> = {
      P1: 'A1', P2: 'B1', P3: 'C2', P4: '', P5: '', P6: '',
    };
    for (const [p, want] of Object.entries(expected)) {
      assert.equal(db.readPersistedPage(p).data, want, `${p} 终值错误`);
    }
    // 全部 7 条 UPDATE 恰好各补偿一次
    const clrs = walRecords(db).filter((r) => r.type === 'CLR');
    assert.equal(clrs.length, 7, '跨两次恢复的补偿总数必须等于 loser 更新数');
  } finally {
    cleanup(dir);
  }
});

test('端到端：空 WAL 恢复为空操作', () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const report = db.recover();
    assert.deepEqual(report.losers, []);
    assert.deepEqual(report.winners, []);
    assert.equal(report.undoCLRs.length, 0);
    assert.equal(report.redoneLSNs.length, 0);
  } finally {
    cleanup(dir);
  }
});
