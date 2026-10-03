import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DBSystem } from '../src/system.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { cleanup, crashAndReopen, tempDir } from './helpers.ts';

test('redo: 重放未刷盘的已提交与未提交修改（历史重复）', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    const t2 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'committed'); // LSN 1
    await db.tx.commit(t1);
    await db.tx.update(t2, 'P2', 'uncommitted'); // LSN 4
    db = crashAndReopen(db); // 崩溃时两页都未刷盘

    const rm = new RecoveryManager(db.wal, db.bufferPool);
    const analysis = rm.analysis();
    const redone = rm.redo(analysis);
    assert.deepEqual(redone, [1, 4], '已提交与未提交的 UPDATE 都必须重放');
    assert.equal(db.bufferPool.fetch('P1').data, 'committed');
    assert.equal(db.bufferPool.fetch('P2').data, 'uncommitted');
    assert.equal(db.bufferPool.fetch('P1').pageLSN, 1);
    assert.equal(db.bufferPool.fetch('P2').pageLSN, 4);
  } finally {
    cleanup(dir);
  }
});

test('redo: pageLSN 不落后的页跳过重放（幂等）', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'v1'); // LSN 1
    db.flush(); // P1 以 pageLSN=1 落盘
    await db.tx.update(t1, 'P1', 'v2'); // LSN 2
    await db.tx.update(t1, 'P2', 'w1'); // LSN 3
    db = crashAndReopen(db);

    const rm = new RecoveryManager(db.wal, db.bufferPool);
    const analysis = rm.analysis();
    const redone = rm.redo(analysis);
    assert.deepEqual(redone, [2, 3], 'LSN 1 已持久化，必须跳过');
    assert.equal(db.bufferPool.fetch('P1').data, 'v2');
  } finally {
    cleanup(dir);
  }
});

test('redo: 重复执行恢复无副作用', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'a');
    await db.tx.commit(t1);
    db = crashAndReopen(db);
    const r1 = db.recover();
    assert.deepEqual(r1.redoneLSNs, [1]);
    const image1 = db.readPersistedPage('P1');

    db = crashAndReopen(db);
    const r2 = db.recover();
    assert.deepEqual(r2.redoneLSNs, [], '第二次恢复不得重放任何记录');
    assert.deepEqual(db.readPersistedPage('P1'), image1, '页镜像不得改变');
  } finally {
    cleanup(dir);
  }
});

test('redo: CLR 同样被重放（补偿记录也是历史）', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir, {
      hooks: {
        afterAppend(rec) {
          // 第一条 CLR 落盘后注入崩溃
          if (rec.type === 'CLR') throw new Error('injected crash');
        },
      },
    });
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'v1'); // LSN 1
    await db.tx.update(t1, 'P2', 'v2'); // LSN 2
    await assert.rejects(db.tx.abort(t1), /injected crash/);
    // 崩溃时：ABORT(LSN3) 与补偿 P2 的 CLR(LSN4) 已落盘，P1 尚未补偿
    db = crashAndReopen(db);

    const rm = new RecoveryManager(db.wal, db.bufferPool);
    const analysis = rm.analysis();
    const redone = rm.redo(analysis);
    assert.ok(redone.includes(4), 'CLR(LSN4) 必须被 Redo 重放');
    assert.equal(
      db.bufferPool.fetch('P2').data,
      '',
      'CLR 重放后 P2 必须回到前镜像',
    );
  } finally {
    cleanup(dir);
  }
});

test('redo: 重建的脏页表是保守超集，pageLSN 检查保证精确跳过', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'a'); // LSN 1
    db.flush(); // P1 落盘，脏页表清空
    await db.tx.commit(t1);
    db = crashAndReopen(db);

    const rm = new RecoveryManager(db.wal, db.bufferPool);
    const analysis = rm.analysis();
    // 崩溃丢失了"已刷盘"的内存信息，Analysis 重建的 DPT 是保守超集
    assert.deepEqual([...analysis.dirtyPageTable.entries()], [['P1', 1]]);
    const redone = rm.redo(analysis);
    assert.deepEqual(redone, [], '盘上 pageLSN=1 不落后于 LSN 1，必须跳过');
    assert.equal(db.readPersistedPage('P1').data, 'a');
  } finally {
    cleanup(dir);
  }
});
