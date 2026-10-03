import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DBSystem } from '../src/system.ts';
import { cleanup, crashAndReopen, recordsOf, tempDir } from './helpers.ts';
import type { CLRRecord } from '../src/types.ts';

test('rollback: 中止事务全链反向补偿，状态与锁无泄漏', async () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'a');
    await db.tx.update(t1, 'P2', 'b');
    await db.tx.update(t1, 'P3', 'c');
    assert.ok(db.lockManager.heldCount > 0, '中止前持有页锁');

    await db.tx.abort(t1);

    // 数据全部回到前镜像
    for (const p of ['P1', 'P2', 'P3']) {
      assert.equal(db.bufferPool.fetch(p).data, '');
    }
    // 日志链：3 UPDATE + ABORT + 3 CLR + END
    const recs = recordsOf(db, t1);
    assert.deepEqual(
      recs.map((r) => r.type),
      ['UPDATE', 'UPDATE', 'UPDATE', 'ABORT', 'CLR', 'CLR', 'CLR', 'END'],
    );
    // CLR 逆序补偿，undoNextLSN 链接正确
    const clrs = recs.filter((r) => r.type === 'CLR') as CLRRecord[];
    assert.deepEqual(
      clrs.map((c) => [c.pageId, c.undoNextLSN]),
      [
        ['P3', 2],
        ['P2', 1],
        ['P1', 0],
      ],
    );
    // 状态编排：事务表清空、锁全部释放
    assert.equal(db.tx.txTable.size, 0, '事务表无泄漏');
    assert.equal(db.lockManager.heldCount, 0, '锁无泄漏');
  } finally {
    cleanup(dir);
  }
});

test('rollback: 空事务中止只写 ABORT+END，无 CLR', async () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.abort(t1);
    const recs = recordsOf(db, t1);
    assert.deepEqual(
      recs.map((r) => r.type),
      ['ABORT', 'END'],
    );
    assert.equal(db.tx.txTable.size, 0);
    assert.equal(db.lockManager.heldCount, 0);
  } finally {
    cleanup(dir);
  }
});

test('rollback: 中止不影响其他事务的已提交数据', async () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    const t2 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'keep');
    await db.tx.commit(t1);
    await db.tx.update(t2, 'P1', 'discard');
    await db.tx.update(t2, 'P2', 'discard');
    await db.tx.abort(t2);
    assert.equal(db.bufferPool.fetch('P1').data, 'keep');
    assert.equal(db.bufferPool.fetch('P2').data, '');
  } finally {
    cleanup(dir);
  }
});

test('rollback: 崩溃发生在中止中途，恢复恰好续扫剩余补偿（不重复补偿）', async () => {
  const dir = tempDir();
  try {
    let clrCount = 0;
    let db = DBSystem.open(dir, {
      hooks: {
        afterAppend(rec) {
          if (rec.type === 'CLR') {
            clrCount += 1;
            if (clrCount === 2) throw new Error('injected crash');
          }
        },
      },
    });
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'v1');
    await db.tx.update(t1, 'P2', 'v2');
    await db.tx.update(t1, 'P3', 'v3');
    await assert.rejects(db.tx.abort(t1), /injected crash/);
    // 崩溃点：P3、P2 已补偿（2 条 CLR），P1 未补偿
    db = crashAndReopen(db);

    const report = db.recover();
    assert.equal(report.undoCLRs.length, 1, '只补偿剩余的 P1');
    assert.equal(report.undoCLRs[0].pageId, 'P1');
    db.flush();
    for (const p of ['P1', 'P2', 'P3']) {
      assert.equal(db.readPersistedPage(p).data, '');
    }
    // 该事务的 CLR 总数 == UPDATE 数，每条 UPDATE 恰好补偿一次
    const clrs = recordsOf(db, t1).filter((r) => r.type === 'CLR');
    assert.equal(clrs.length, 3, '不得重复补偿');
    assert.equal(recordsOf(db, t1).at(-1)!.type, 'END');
  } finally {
    cleanup(dir);
  }
});

test('rollback: 中止后重跑恢复幂等，无新增 CLR', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'x');
    await db.tx.abort(t1);
    db.flush();
    const walSize = db.wal.lastLSN;
    db = crashAndReopen(db);
    const report = db.recover();
    assert.equal(report.undoCLRs.length, 0);
    assert.equal(report.losers.length, 0);
    assert.equal(db.wal.lastLSN, walSize, '恢复不得追加任何日志');
  } finally {
    cleanup(dir);
  }
});

test('rollback: 非法状态迁移被拒绝', async () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'a');
    await db.tx.commit(t1);
    await assert.rejects(db.tx.abort(t1), /unknown tx/, '已结束事务不得中止');
    await assert.rejects(db.tx.update(t1, 'P1', 'b'), /unknown tx/);
    await assert.rejects(db.tx.commit(t1), /unknown tx/);
    assert.equal(db.lockManager.heldCount, 0, '异常路径不得泄漏锁');
  } finally {
    cleanup(dir);
  }
});

test('rollback: 长事务多步写后失败，补偿严格逆序且逐步可见', async () => {
  const dir = tempDir();
  try {
    const db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    const steps: [string, string][] = [
      ['S1', 'step1-done'],
      ['S2', 'step2-done'],
      ['S3', 'step3-done'],
      ['S4', 'step4-done'],
      ['S5', 'step5-done'],
    ];
    for (const [p, v] of steps) await db.tx.update(t1, p, v);
    // 模拟长流程第 6 步失败 → 触发整体逆向补偿
    await db.tx.abort(t1);
    const clrs = recordsOf(db, t1).filter((r) => r.type === 'CLR') as CLRRecord[];
    assert.deepEqual(
      clrs.map((c) => c.pageId),
      ['S5', 'S4', 'S3', 'S2', 'S1'],
      '补偿顺序必须与执行顺序严格相反',
    );
    for (const [p] of steps) {
      assert.equal(db.bufferPool.fetch(p).data, '', `${p} 已补偿`);
    }
    assert.equal(db.tx.txTable.size, 0);
    assert.equal(db.lockManager.heldCount, 0);
  } finally {
    cleanup(dir);
  }
});
