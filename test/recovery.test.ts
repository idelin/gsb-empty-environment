import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/database.ts';
import { WAL } from '../src/wal.ts';
import { PageStore } from '../src/storage.ts';
import { analyze, redo, undo } from '../src/recovery.ts';

async function buildCrashedDatabase(): Promise<{ dir: string }> {
  const dir = await mkdtemp(join(tmpdir(), 'aries-'));
  const db = await Database.open(dir);

  const t1 = await db.begin();
  await db.set(t1, 1, 5, 100);
  await db.set(t1, 2, 7, 200);
  await db.commit(t1);

  await db.checkpoint();

  const t2 = await db.begin();
  await db.set(t2, 1, 5, 150);
  await db.commit(t2);

  const t3 = await db.begin();
  await db.set(t3, 2, 7, 250);
  await db.set(t3, 3, 1, 300);

  const t4 = await db.begin();
  await db.set(t4, 1, 5, 175);

  const t5 = await db.begin();
  await db.set(t5, 3, 2, 400);
  await db.set(t5, 2, 9, 500);

  await db.wal.flush();
  db.crash();
  return { dir };
}

test('ARIES 三阶段恢复：回滚 3 个未提交事务，持久化 2 个已提交事务', async () => {
  const { dir } = await buildCrashedDatabase();
  try {
    const db = await Database.open(dir);
    const report = await db.recover();

    assert.equal(report.analysis.txTable.size, 3);
    assert.deepEqual([...report.analysis.txTable.keys()].sort((a, b) => a - b), [3, 4, 5]);
    for (const entry of report.analysis.txTable.values()) assert.equal(entry.status, 'running');
    assert.deepEqual([...report.analysis.dpt.keys()].sort((a, b) => a - b), [1, 2, 3]);

    assert.equal(report.clrLSNs.length, 5);
    assert.deepEqual([...report.ended].sort((a, b) => a - b), [3, 4, 5]);

    assert.equal(db.readSlot(1, 5), 150);
    assert.equal(db.readSlot(2, 7), 200);
    assert.equal(db.readSlot(2, 9), 0);
    assert.equal(db.readSlot(3, 1), 0);
    assert.equal(db.readSlot(3, 2), 0);

    const log = await WAL.readAll(db.wal.path);
    const clrs = log.filter((r) => r.kind === 'clr');
    const ends = log.filter((r) => r.kind === 'end');
    assert.equal(clrs.length, 5);
    assert.equal(ends.filter((r) => [3, 4, 5].includes(r.txId)).length, 3);
    for (const clr of clrs) {
      assert.ok(clr.undoNextLSN !== undefined);
      assert.ok(clr.prevLSN < clr.lsn);
    }

    const stats = db.stats;
    assert.equal(stats.txTableSize, 0);
    assert.equal(stats.dptSize, 0);
    assert.equal(stats.walBuffered, 0);
    assert.equal(stats.dirtyPages, 0);
    await db.shutdown();
    assert.equal(db.stats.accountedBytes, 0);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('恢复幂等：崩溃后再次恢复不产生新的 CLR，结果不变', async () => {
  const { dir } = await buildCrashedDatabase();
  try {
    const first = await Database.open(dir);
    await first.recover();
    await first.shutdown();

    const second = await Database.open(dir);
    const report = await second.recover();
    assert.equal(report.analysis.txTable.size, 0);
    assert.equal(report.redone.length, 0);
    assert.equal(report.clrLSNs.length, 0);
    assert.equal(report.ended.length, 0);
    assert.equal(second.readSlot(1, 5), 150);
    assert.equal(second.readSlot(2, 7), 200);
    assert.equal(second.readSlot(2, 9), 0);
    assert.equal(second.readSlot(3, 1), 0);
    assert.equal(second.readSlot(3, 2), 0);
    await second.shutdown();
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('分阶段断言：Analysis 精确重建 DPT/TT，Redo 重复历史，Undo 逆序补偿', async () => {
  const { dir } = await buildCrashedDatabase();
  try {
    const walPath = join(dir, 'wal.log');
    const pagesPath = join(dir, 'pages.json');
    const log = await WAL.readAll(walPath);

    const analysis = analyze(log);
    assert.deepEqual([...analysis.dpt.entries()].sort((a, b) => a[0] - b[0]), [[1, 2], [2, 3], [3, 14]]);
    assert.equal(analysis.txTable.get(3)?.lastLSN, 14);
    assert.equal(analysis.txTable.get(4)?.lastLSN, 16);
    assert.equal(analysis.txTable.get(5)?.lastLSN, 19);
    assert.ok(!analysis.txTable.has(1));
    assert.ok(!analysis.txTable.has(2));

    const store = await PageStore.open(pagesPath);
    const wal = await WAL.open(walPath);
    const redone = redo(log, analysis.dpt, store);
    assert.deepEqual(redone, [9, 13, 14, 16, 18, 19]);
    assert.equal(store.getSlot(1, 5), 175);
    assert.equal(store.getSlot(2, 7), 250);
    assert.equal(store.getSlot(2, 9), 500);
    assert.equal(store.getSlot(3, 1), 300);
    assert.equal(store.getSlot(3, 2), 400);

    const { clrLSNs, ended } = undo(log, analysis.txTable, wal, store);
    assert.equal(clrLSNs.length, 5);
    assert.deepEqual([...ended].sort((a, b) => a - b), [3, 4, 5]);
    assert.equal(store.getSlot(1, 5), 150);
    assert.equal(store.getSlot(2, 7), 200);
    assert.equal(store.getSlot(2, 9), 0);
    assert.equal(store.getSlot(3, 1), 0);
    assert.equal(store.getSlot(3, 2), 0);

    await store.flushAll();
    await wal.flush();
    store.evictAll();
    assert.equal(store.accountant.bytes, 0);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('Checkpoint 快照携带活跃事务时 Analysis 正确播种', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'aries-'));
  try {
    const db = await Database.open(dir);
    const t1 = await db.begin();
    await db.set(t1, 7, 3, 42);
    await db.checkpoint();
    await db.set(t1, 7, 4, 43);
    await db.wal.flush();
    db.crash();

    const recovered = await Database.open(dir);
    const report = await recovered.recover();
    assert.equal(report.analysis.txTable.size, 1);
    assert.equal(report.analysis.txTable.get(1)?.status, 'running');
    assert.equal(report.clrLSNs.length, 2);
    assert.equal(recovered.readSlot(7, 3), 0);
    assert.equal(recovered.readSlot(7, 4), 0);
    await recovered.shutdown();
    assert.equal(recovered.stats.accountedBytes, 0);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
