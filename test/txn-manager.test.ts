/** 事务管理器：协议状态机迁移约束、WAL 先行、ABORT 补偿、泄漏防护。 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { WalWriter, readWal } from '../src/wal.ts';
import { BufferPool, DiskStorage } from '../src/storage.ts';
import { TransactionManager } from '../src/txn-manager.ts';
import { LogType } from '../src/log-record.ts';

function setup(dir: string) {
  const walPath = join(dir, 'wal.log');
  const wal = new WalWriter(walPath);
  const buffer = new BufferPool(new DiskStorage(join(dir, 'db.json')));
  const tm = new TransactionManager(wal, buffer);
  return { walPath, wal, buffer, tm };
}

function withTemp(fn: (dir: string) => void): () => void {
  return () => {
    const dir = mkdtempSync(join(tmpdir(), 'aries-tm-'));
    try {
      fn(dir);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  };
}

test('状态机：非法迁移全部拒绝（重复提交/提交后更新/未知事务/重复中止）', withTemp((dir) => {
  const { tm } = setup(dir);
  const t1 = tm.begin();
  tm.update(t1, 'P1', 1);
  tm.commit(t1);
  assert.throws(() => tm.commit(t1), /unknown transaction/);
  assert.throws(() => tm.update(t1, 'P1', 2), /unknown transaction/);
  assert.throws(() => tm.abort(t1), /unknown transaction/);
  assert.throws(() => tm.update(999, 'P1', 1), /unknown transaction 999/);

  const t2 = tm.begin();
  tm.abort(t2);
  assert.throws(() => tm.abort(t2), /unknown transaction/);
  assert.throws(() => tm.commit(t2), /unknown transaction/);
  tm.close();
}));

test('WAL 先行：UPDATE 日志的 before/after 与 prevLSN 链严格正确', withTemp((dir) => {
  const { tm, walPath } = setup(dir);
  const t1 = tm.begin();
  tm.update(t1, 'P1', 10);
  tm.update(t1, 'P1', 20);
  tm.update(t1, 'P2', 7);
  tm.commit(t1);
  tm.close();

  const records = readWal(walPath);
  assert.deepEqual(
    records.map((r) => r.type),
    [LogType.Update, LogType.Update, LogType.Update, LogType.Commit, LogType.End],
  );
  const [u1, u2, u3, c, e] = records;
  assert.deepEqual([u1.prevLSN, u2.prevLSN, u3.prevLSN, c.prevLSN, e.prevLSN], [null, 1, 2, 3, 4]);
  assert.equal(u1.type === LogType.Update && u1.before, 0);
  assert.equal(u2.type === LogType.Update && u2.before, 10);
  assert.equal(u3.type === LogType.Update && u3.before, 0);
}));

test('ABORT：写 ABORT + 逐条 CLR + END，页面恢复到事务前状态', withTemp((dir) => {
  const { tm, buffer, walPath } = setup(dir);
  const t1 = tm.begin();
  tm.update(t1, 'P1', 100);
  tm.commit(t1);

  const t2 = tm.begin();
  tm.update(t2, 'P1', 200);
  tm.update(t2, 'P2', 50);
  tm.abort(t2);

  assert.equal(buffer.read('P1').value, 100, '中止事务的修改必须被补偿');
  assert.equal(buffer.read('P2').value, 0);
  const types = readWal(walPath).map((r) => r.type);
  assert.deepEqual(types, [
    LogType.Update, LogType.Commit, LogType.End, // T1
    LogType.Update, LogType.Update,              // T2 的修改
    LogType.Abort, LogType.Clr, LogType.Clr, LogType.End, // T2 的回滚
  ]);
  tm.close();
}));

test('提交点 fsync 且事务表清空；带活跃事务关闭视为泄漏并拒绝', withTemp((dir) => {
  const { tm } = setup(dir);
  const t1 = tm.begin();
  tm.update(t1, 'P1', 1);
  assert.throws(() => tm.close(), /active transactions/);
  tm.abort(t1);
  tm.close();
  assert.equal(tm.isClosed, true);
  assert.throws(() => tm.begin(), /closed/);
}));

test('检查点记录携带事务表与脏页表快照', withTemp((dir) => {
  const { tm, buffer, walPath } = setup(dir);
  const t1 = tm.begin();
  tm.update(t1, 'P1', 1);
  tm.commit(t1);
  const t2 = tm.begin();
  tm.update(t2, 'P2', 2);
  buffer.flush('P1');
  tm.checkpoint();
  tm.abort(t2);
  tm.close();

  const records = readWal(walPath);
  const endCkpt = records.find((r) => r.type === LogType.EndCheckpoint);
  assert.ok(endCkpt && endCkpt.type === LogType.EndCheckpoint);
  assert.deepEqual(endCkpt.txnTable, [{ txnId: 2, status: 'RUNNING', lastLSN: 4 }]);
  assert.deepEqual(endCkpt.dirtyPageTable, [['P2', 4]]);
}));

test('空事务（仅提交，无更新）不污染脏页表', withTemp((dir) => {
  const { tm, buffer, walPath } = setup(dir);
  const t1 = tm.begin();
  tm.commit(t1);
  tm.close();
  assert.equal(buffer.hasDirtyPages(), false);
  const types = readWal(walPath).map((r) => r.type);
  assert.deepEqual(types, [LogType.Commit, LogType.End]);
}));
