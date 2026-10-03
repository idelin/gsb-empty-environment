import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { AsyncMutex } from '../src/mutex.ts';
import { SparseVector } from '../src/sparse.ts';
import { Database } from '../src/database.ts';
import { WAL } from '../src/wal.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

test('AsyncMutex：64 个并发工作者 × 250 次递增结果精确', async () => {
  const mutex = new AsyncMutex();
  let counter = 0;
  const workers = 64;
  const perWorker = 250;
  await Promise.all(
    Array.from({ length: workers }, async (_, w) => {
      for (let i = 0; i < perWorker; i++) {
        await mutex.runExclusive(async () => {
          const snapshot = counter;
          if ((w + i) % 7 === 0) await sleep(0);
          counter = snapshot + 1;
        });
      }
    }),
  );
  assert.equal(counter, workers * perWorker);
});

test('并发写入 SparseVector：互斥保护下无丢失更新', async () => {
  const mutex = new AsyncMutex();
  const vector = new SparseVector();
  const workers = 32;
  const perWorker = 200;
  await Promise.all(
    Array.from({ length: workers }, async (_, w) => {
      for (let i = 0; i < perWorker; i++) {
        const index = w * perWorker + i;
        await mutex.runExclusive(async () => {
          if ((index & 3) === 0) await sleep(0);
          vector.set(index, index + 1);
        });
      }
    }),
  );
  assert.equal(vector.size, workers * perWorker);
  for (let i = 0; i < workers * perWorker; i++) assert.equal(vector.get(i), i + 1);
});

test('并发事务：WAL 的 LSN 连续且各事务 prevLSN 链完整', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'aries-conc-'));
  try {
    const db = await Database.open(dir);
    const txCount = 16;
    const opsPerTx = 20;
    const txIds = await Promise.all(Array.from({ length: txCount }, () => db.begin()));
    await Promise.all(
      txIds.map(async (txId, t) => {
        for (let i = 0; i < opsPerTx; i++) {
          await db.set(txId, 100 + t, i, t * 1000 + i);
          if ((t + i) % 5 === 0) await sleep(0);
        }
        await db.commit(txId);
      }),
    );
    await db.wal.flush();

    const log = await WAL.readAll(db.wal.path);
    log.forEach((record, i) => assert.equal(record.lsn, i + 1));

    const lastLSNByTx = new Map<number, number>();
    for (const record of log) {
      if (record.txId === 0) continue;
      const expected = lastLSNByTx.get(record.txId) ?? 0;
      assert.equal(record.prevLSN, expected, `tx ${record.txId} lsn ${record.lsn}`);
      lastLSNByTx.set(record.txId, record.lsn);
    }

    for (let t = 0; t < txCount; t++) {
      for (let i = 0; i < opsPerTx; i++) {
        assert.equal(db.readSlot(100 + t, i), t * 1000 + i);
      }
    }
    assert.equal(db.stats.txTableSize, 0);
    await db.shutdown();
    assert.equal(db.stats.accountedBytes, 0);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('并发事务崩溃后恢复：已提交者持久化，未提交者回滚', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'aries-conc-crash-'));
  try {
    const db = await Database.open(dir);
    const committed = await Promise.all(Array.from({ length: 8 }, () => db.begin()));
    const losers = await Promise.all(Array.from({ length: 8 }, () => db.begin()));
    await Promise.all([
      ...committed.map(async (txId, t) => {
        for (let i = 0; i < 10; i++) await db.set(txId, 200 + t, i, 1);
        await db.commit(txId);
      }),
      ...losers.map(async (txId, t) => {
        for (let i = 0; i < 10; i++) await db.set(txId, 300 + t, i, 999);
      }),
    ]);
    await db.wal.flush();
    db.crash();

    const recovered = await Database.open(dir);
    const report = await recovered.recover();
    assert.equal(report.analysis.txTable.size, 8);
    assert.equal(report.clrLSNs.length, 80);
    for (let t = 0; t < 8; t++) {
      for (let i = 0; i < 10; i++) {
        assert.equal(recovered.readSlot(200 + t, i), 1);
        assert.equal(recovered.readSlot(300 + t, i), 0);
      }
    }
    await recovered.shutdown();
    assert.equal(recovered.stats.accountedBytes, 0);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
