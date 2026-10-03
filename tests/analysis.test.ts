import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DBSystem } from '../src/system.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { cleanup, crashAndReopen, tempDir } from './helpers.ts';

test('analysis: 无检查点时从日志起点重建事务表与脏页表', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    const t2 = db.tx.begin();
    const t3 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'x1'); // LSN 1
    await db.tx.update(t2, 'P2', 'x2'); // LSN 2
    await db.tx.commit(t2); // LSN 3 COMMIT, LSN 4 END
    await db.tx.update(t3, 'P3', 'x3'); // LSN 5
    db = crashAndReopen(db);

    const a = new RecoveryManager(db.wal, db.bufferPool).analysis();
    assert.equal(a.checkpointLSN, null);
    assert.equal(a.redoLSN, 1);
    assert.deepEqual(
      [...a.txTable.values()].map((e) => [e.txId, e.status, e.lastLSN]),
      [
        [t1, 'RUNNING', 1],
        [t3, 'RUNNING', 5],
      ],
      '已 END 的事务必须从事务表移除',
    );
    assert.deepEqual(
      [...a.dirtyPageTable.entries()].sort(),
      [
        ['P1', 1],
        ['P2', 2],
        ['P3', 5],
      ],
    );
    assert.equal(db.lockManager.heldCount, 0, '锁无泄漏');
  } finally {
    cleanup(dir);
  }
});

test('analysis: 从最近模糊检查点快照出发并合并其后的日志', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    const t2 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'a'); // LSN 1
    await db.tx.update(t2, 'P2', 'b'); // LSN 2
    db.checkpoint(); // LSN 3 BEGIN, LSN 4 END_CHECKPOINT
    await db.tx.commit(t1); // LSN 5,6
    const t3 = db.tx.begin();
    await db.tx.update(t3, 'P3', 'c'); // LSN 7
    await db.tx.update(t2, 'P2', 'b2'); // LSN 8
    db = crashAndReopen(db);

    const a = new RecoveryManager(db.wal, db.bufferPool).analysis();
    assert.equal(a.checkpointLSN, 4);
    // t1 已提交并 END → 不在表中；t2 仍 RUNNING；t3 检查点后出现
    assert.deepEqual(
      [...a.txTable.values()].map((e) => [e.txId, e.status, e.lastLSN]),
      [
        [t2, 'RUNNING', 8],
        [t3, 'RUNNING', 7],
      ],
    );
    // DPT：快照中的 P1@1, P2@2 与检查点后的 P3@7 合并；P2 保留最早 recLSN=2
    assert.deepEqual(
      [...a.dirtyPageTable.entries()].sort(),
      [
        ['P1', 1],
        ['P2', 2],
        ['P3', 7],
      ],
    );
    assert.equal(a.redoLSN, 1);
  } finally {
    cleanup(dir);
  }
});

test('analysis: 崩溃发生在 ABORT 之后，事务状态保持 ABORTING', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir, {
      hooks: {
        afterAppend(rec) {
          if (rec.type === 'ABORT') throw new Error('injected crash');
        },
      },
    });
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'v1');
    await db.tx.update(t1, 'P2', 'v2');
    await assert.rejects(db.tx.abort(t1), /injected crash/);
    db = crashAndReopen(db);

    const a = new RecoveryManager(db.wal, db.bufferPool).analysis();
    const entry = a.txTable.get(t1);
    assert.ok(entry, '中止中的事务必须保留在事务表');
    assert.equal(entry.status, 'ABORTING');
    assert.equal(entry.lastLSN, 3, 'lastLSN 指向 ABORT 记录');
  } finally {
    cleanup(dir);
  }
});

test('analysis: 多个检查点时只使用最近的一个', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'a');
    db.checkpoint();
    await db.tx.commit(t1);
    db.flush();
    db.checkpoint(); // 第二个检查点：事务表应为空快照
    const t2 = db.tx.begin();
    await db.tx.update(t2, 'P2', 'b');
    db = crashAndReopen(db);

    const a = new RecoveryManager(db.wal, db.bufferPool).analysis();
    assert.deepEqual(
      [...a.txTable.values()].map((e) => e.txId),
      [t2],
      't1 在第二个检查点前已结束，不得出现',
    );
    assert.deepEqual([...a.dirtyPageTable.entries()], [['P2', 8]]);
  } finally {
    cleanup(dir);
  }
});
