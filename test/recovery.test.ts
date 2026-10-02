import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { Database } from '../src/database.ts';

function tempDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'aries-wal-'));
}

test('已提交事务在崩溃后通过 Redo 恢复（NO-FORCE：提交不刷页）', () => {
  const dir = tempDir();
  const db = Database.open(dir);
  const tx = db.begin();
  db.update(tx, 'accounts', 'A', 1000);
  db.update(tx, 'accounts', 'B', 2000);
  db.commit(tx);
  // 不刷页，直接断电：已提交数据只存在于 WAL 中
  db.crash();

  const { db: db2, report } = Database.recover(dir);
  assert.equal(db2.read('accounts', 'A'), 1000);
  assert.equal(db2.read('accounts', 'B'), 2000);
  assert.equal(report.redo.replayed, 2);
  assert.deepEqual(report.analysis.losers, []);
});

test('未提交事务在崩溃后被 Undo 回滚', () => {
  const dir = tempDir();
  const db = Database.open(dir);
  const t1 = db.begin();
  db.update(t1, 'accounts', 'A', 1000);
  db.commit(t1);
  db.flushAll(); // 已提交数据落盘

  const t2 = db.begin();
  db.update(t2, 'accounts', 'A', 500); // 未提交的修改
  db.crash();

  const { db: db2, report } = Database.recover(dir);
  assert.equal(db2.read('accounts', 'A'), 1000);
  assert.deepEqual(report.analysis.losers, [t2]);
  assert.equal(report.undo.clrCount, 1);
});

test('批量更新中途断电：已提交批次保留，进行中批次完整回滚', () => {
  const dir = tempDir();
  const db = Database.open(dir);

  // 初始化账户
  const t0 = db.begin();
  db.update(t0, 'accounts', 'A', 1000);
  db.update(t0, 'accounts', 'B', 1000);
  db.commit(t0);

  // 批次 1：100 笔转账，全部提交
  const t1 = db.begin();
  for (let i = 0; i < 100; i++) {
    db.update(t1, 'accounts', 'A', 1000 - (i + 1));
    db.update(t1, 'accounts', 'B', 1000 + (i + 1));
  }
  db.commit(t1);

  // 批次 2：更新到一半突然断电（模拟进程崩溃 / 电源故障）
  const t2 = db.begin();
  for (let i = 0; i < 37; i++) {
    db.update(t2, 'accounts', 'A', 900 - (i + 1));
    db.update(t2, 'accounts', 'B', 1100 + (i + 1));
  }
  db.crash(); // 断电：缓冲池与未 flush 的日志全部丢失

  const { db: db2, report } = Database.recover(dir);
  // 批次 1（已提交）必须保留
  assert.equal(db2.read('accounts', 'A'), 900);
  assert.equal(db2.read('accounts', 'B'), 1100);
  // 批次 2 是 loser，被完整回滚
  assert.deepEqual(report.analysis.losers, [t2]);
  assert.equal(report.undo.losers.length, 1);
});

test('STEAL：未提交脏页被刷盘后，恢复时 Undo 清理磁盘上的脏数据', () => {
  const dir = tempDir();
  const db = Database.open(dir);
  const t1 = db.begin();
  db.update(t1, 'accounts', 'A', 1000);
  db.commit(t1);
  db.flushAll();

  const t2 = db.begin();
  db.update(t2, 'accounts', 'A', 1);
  db.flushPage('accounts'); // 未提交的脏页被置换写盘（STEAL）
  db.crash();

  // 确认磁盘上确实残留了未提交数据
  const disk = JSON.parse(fs.readFileSync(path.join(dir, 'data.json'), 'utf8'));
  assert.equal(disk['accounts'].data['A'], 1);

  const { db: db2 } = Database.recover(dir);
  assert.equal(db2.read('accounts', 'A'), 1000);
  const disk2 = JSON.parse(fs.readFileSync(path.join(dir, 'data.json'), 'utf8'));
  assert.equal(disk2['accounts'].data['A'], 1000);
});

test('主动 abort 回滚全部修改', () => {
  const dir = tempDir();
  const db = Database.open(dir);
  const t1 = db.begin();
  db.update(t1, 'p1', 'x', 1);
  db.commit(t1);

  const t2 = db.begin();
  db.update(t2, 'p1', 'x', 2);
  db.update(t2, 'p1', 'y', 3);
  db.abort(t2);

  assert.equal(db.read('p1', 'x'), 1);
  assert.equal(db.read('p1', 'y'), undefined);
});

test('检查点之后的崩溃恢复（Analysis 从 CHECKPOINT 起步）', () => {
  const dir = tempDir();
  const db = Database.open(dir);
  const t1 = db.begin();
  db.update(t1, 'p1', 'a', 1);
  db.commit(t1);
  db.flushAll();
  db.checkpoint();

  const t2 = db.begin();
  db.update(t2, 'p1', 'b', 2);
  db.commit(t2); // 已提交未刷盘

  const t3 = db.begin();
  db.update(t3, 'p1', 'c', 3); // 未提交
  db.crash();

  const { db: db2, report } = Database.recover(dir);
  assert.ok(report.analysis.checkpointLSN > 0);
  assert.equal(db2.read('p1', 'a'), 1);
  assert.equal(db2.read('p1', 'b'), 2);
  assert.equal(db2.read('p1', 'c'), undefined);
});

test('恢复幂等：恢复过程中再次崩溃，二次恢复结果一致（CLR/undoNextLSN）', () => {
  const dir = tempDir();
  const db = Database.open(dir);
  const t1 = db.begin();
  db.update(t1, 'p1', 'x', 42);
  db.commit(t1);
  const t2 = db.begin();
  db.update(t2, 'p1', 'x', 1);
  db.update(t2, 'p1', 'y', 2);
  db.crash();

  // 第一次恢复
  const first = Database.recover(dir);
  assert.equal(first.db.read('p1', 'x'), 42);
  assert.equal(first.db.read('p1', 'y'), undefined);
  first.db.crash(); // 恢复完成后再次断电

  // 第二次恢复：不应重复回滚，结果一致
  const second = Database.recover(dir);
  assert.equal(second.db.read('p1', 'x'), 42);
  assert.equal(second.db.read('p1', 'y'), undefined);
  assert.deepEqual(second.report.analysis.losers, []);
});

test('新增键的 Undo 会删除该键（前镜像不存在）', () => {
  const dir = tempDir();
  const db = Database.open(dir);
  const t1 = db.begin();
  db.update(t1, 'p1', 'newKey', 'newValue');
  db.flushPage('p1'); // 未提交的新键落盘
  db.crash();

  const { db: db2 } = Database.recover(dir);
  assert.equal(db2.read('p1', 'newKey'), undefined);
});

test('多个 loser 事务交错写入，全部正确回滚', () => {
  const dir = tempDir();
  const db = Database.open(dir);
  const t0 = db.begin();
  db.update(t0, 'p1', 'x', 0);
  db.update(t0, 'p2', 'y', 0);
  db.commit(t0);
  db.flushAll();

  const t1 = db.begin();
  const t2 = db.begin();
  db.update(t1, 'p1', 'x', 11);
  db.update(t2, 'p1', 'x', 22);
  db.update(t1, 'p2', 'y', 111);
  db.update(t2, 'p2', 'y', 222);
  db.flushPage('p1'); // 部分未提交数据落盘
  db.crash();

  const { db: db2, report } = Database.recover(dir);
  assert.equal(db2.read('p1', 'x'), 0);
  assert.equal(db2.read('p2', 'y'), 0);
  assert.deepEqual(report.analysis.losers.sort(), [t1, t2].sort());
});
