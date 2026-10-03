import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DBSystem } from '../src/system.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { cleanup, crashAndReopen, recordsOf, tempDir } from './helpers.ts';

test('undo: 单个 loser 全链反向补偿，CLR 链与 END 完整', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    const t2 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'X'); // LSN 1
    await db.tx.commit(t1); // LSN 2,3
    await db.tx.update(t2, 'P1', 'Y'); // LSN 4
    await db.tx.update(t2, 'P2', 'Z'); // LSN 5
    db = crashAndReopen(db);

    const report = db.recover();
    assert.deepEqual(report.losers, [t2]);
    assert.equal(report.undoCLRs.length, 2);
    // 逆序：先补偿 LSN5(P2)，再补偿 LSN4(P1)
    assert.deepEqual(
      report.undoCLRs.map((c) => [c.pageId, c.undoNextLSN]),
      [
        ['P2', 4],
        ['P1', 0],
      ],
    );
    // CLR 的 after 必须是被撤销记录的前镜像
    assert.equal(report.undoCLRs[0].after, '');
    assert.equal(report.undoCLRs[1].after, 'X');

    db.flush();
    assert.equal(db.readPersistedPage('P1').data, 'X', '已提交数据保留');
    assert.equal(db.readPersistedPage('P2').data, '', '未提交数据回滚');

    // 事务收尾：END 记录存在，事务表清空
    const t2recs = recordsOf(db, t2);
    assert.equal(t2recs[t2recs.length - 1].type, 'END');
    assert.equal(report.analysis.txTable.size, 0, 'Undo 后事务表必须为空');
  } finally {
    cleanup(dir);
  }
});

test('undo: 多 loser 按全局 LSN 逆序补偿', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    const t2 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'a'); // LSN 1
    await db.tx.update(t2, 'P2', 'b'); // LSN 2
    await db.tx.update(t1, 'P3', 'c'); // LSN 3
    db = crashAndReopen(db);

    const report = db.recover();
    assert.deepEqual(
      report.undoCLRs.map((c) => c.pageId),
      ['P3', 'P2', 'P1'],
      '必须按 LSN 降序处理（最大堆调度）',
    );
    db.flush();
    for (const p of ['P1', 'P2', 'P3']) {
      assert.equal(db.readPersistedPage(p).data, '');
    }
  } finally {
    cleanup(dir);
  }
});

test('undo: 已提交未写 END 的 winner 补写 END，数据保留', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir, {
      hooks: {
        afterAppend(rec) {
          if (rec.type === 'COMMIT') throw new Error('injected crash');
        },
      },
    });
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'committed');
    await assert.rejects(db.tx.commit(t1), /injected crash/);
    // 崩溃点：COMMIT 已落盘，END 未写
    db = crashAndReopen(db);

    const report = db.recover();
    assert.deepEqual(report.winners, [t1]);
    assert.deepEqual(report.losers, []);
    assert.equal(report.undoCLRs.length, 0, 'winner 不得被补偿');
    const t1recs = recordsOf(db, t1);
    assert.equal(t1recs[t1recs.length - 1].type, 'END', '恢复必须补写 END');
    assert.equal(db.readPersistedPage('P1').data, 'committed');
  } finally {
    cleanup(dir);
  }
});

test('undo: 崩溃发生在恢复 Undo 中途，重启恢复恰好完成剩余补偿', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'v1'); // LSN 1
    await db.tx.update(t1, 'P2', 'v2'); // LSN 2
    await db.tx.update(t1, 'P3', 'v3'); // LSN 3
    db = crashAndReopen(db);

    // 第一次恢复：在第 2 条 CLR 落盘后注入崩溃
    let clrCount = 0;
    db.recover({
      afterAppend(rec) {
        if (rec.type === 'CLR') {
          clrCount += 1;
          if (clrCount === 2) throw new Error('injected crash during undo');
        }
      },
    });
    assert.fail('should have thrown');
  } catch (e) {
    assert.match(String(e), /injected crash during undo/);
  }
  try {
    // 崩溃现场：P3、P2 已被 CLR 补偿（LSN 4,5），P1 未补偿
    const db2 = DBSystem.open(dir);
    const report = db2.recover();
    assert.equal(report.undoCLRs.length, 1, '只剩 P1 待补偿，不得重复补偿');
    assert.equal(report.undoCLRs[0].pageId, 'P1');
    db2.flush();
    for (const p of ['P1', 'P2', 'P3']) {
      assert.equal(db2.readPersistedPage(p).data, '', `${p} 必须回滚到前镜像`);
    }
    // 幂等：第三次恢复无任何动作
    const db3 = crashAndReopen(db2);
    const report3 = db3.recover();
    assert.equal(report3.undoCLRs.length, 0);
    assert.equal(report3.redoneLSNs.length, 0);
    cleanup(dir);
  } catch (e) {
    cleanup(dir);
    throw e;
  }
});

test('undo: loser 的更新已部分刷盘时回滚仍正确', async () => {
  const dir = tempDir();
  try {
    let db = DBSystem.open(dir);
    const t1 = db.tx.begin();
    await db.tx.update(t1, 'P1', 'committed');
    await db.tx.commit(t1);
    db.flush(); // P1='committed' 落盘
    const t2 = db.tx.begin();
    await db.tx.update(t2, 'P1', 'dirty');
    db.flush(); // loser 的脏页也落盘了（pageLSN 推进）
    db = crashAndReopen(db);

    const report = db.recover();
    assert.deepEqual(report.losers, [t2]);
    db.flush();
    assert.equal(
      db.readPersistedPage('P1').data,
      'committed',
      '即使脏页已落盘，Undo 也必须将其改回前镜像',
    );
    assert.ok(
      db.readPersistedPage('P1').pageLSN >= report.undoCLRs[0].lsn,
      'CLR 的 LSN 必须推进 pageLSN',
    );
  } finally {
    cleanup(dir);
  }
});
