import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Database } from '../src/database.ts';
import { WAL } from '../src/wal.ts';
import { SparseVector, MemoryAccountant } from '../src/sparse.ts';

test('恢复完成后无残留：事务表空、脏页零、WAL 缓冲空、内存归零', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'aries-leak-'));
  try {
    const db = await Database.open(dir);
    const t1 = await db.begin();
    await db.set(t1, 1, 1, 10);
    await db.commit(t1);
    const t2 = await db.begin();
    await db.set(t2, 2, 2, 20);
    await db.wal.flush();
    db.crash();

    const recovered = await Database.open(dir);
    const report = await recovered.recover();
    assert.equal(report.clrLSNs.length, 1);

    const stats = recovered.stats;
    assert.equal(stats.txTableSize, 0);
    assert.equal(stats.dptSize, 0);
    assert.equal(stats.walBuffered, 0);
    assert.equal(stats.dirtyPages, 0);

    await recovered.shutdown();
    assert.equal(recovered.stats.accountedBytes, 0);
    assert.equal(recovered.store.frameCount, 0);

    const log = await WAL.readAll(recovered.wal.path);
    const openTx = new Set<number>();
    for (const record of log) {
      if (record.kind === 'begin') openTx.add(record.txId);
      if (record.kind === 'end') openTx.delete(record.txId);
    }
    assert.equal(openTx.size, 0);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('WAL 缓冲在 flush 后清空，崩溃丢弃未刷缓冲', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'aries-wal-'));
  try {
    const walPath = join(dir, 'wal.log');
    const wal = await WAL.open(walPath);
    wal.append({ kind: 'begin', txId: 1, prevLSN: 0 });
    assert.equal(wal.bufferedCount, 1);
    await wal.flush();
    assert.equal(wal.bufferedCount, 0);
    wal.append({ kind: 'update', txId: 1, prevLSN: 1, pageId: 1, slot: 0, before: 0, after: 5 });
    wal.discardBuffer();
    assert.equal(wal.bufferedCount, 0);
    const log = await WAL.readAll(walPath);
    assert.equal(log.length, 1);
    assert.equal(log[0].kind, 'begin');
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('SparseVector 多次自适应转换后内存记账精确归零', () => {
  const accountant = new MemoryAccountant();
  const v = new SparseVector({ accountant });
  for (let round = 0; round < 5; round++) {
    for (let i = 0; i < 200; i++) v.set(i, i + round);
    assert.equal(v.mode, 'dense');
    for (let i = 0; i < 195; i++) v.delete(i);
    assert.equal(v.mode, 'sparse');
    v.clear();
    assert.equal(accountant.bytes, 0, `round ${round}`);
  }
});
