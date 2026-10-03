import assert from 'node:assert/strict';
import test from 'node:test';
import { mulberry32 } from '../src/util.ts';
import {
  assertNoLeaks,
  assertPageLSNConsistent,
  boot,
  committedTxnIds,
  crash,
  recover,
} from './helpers.ts';

const yieldCtl = () => new Promise<void>((resolve) => setImmediate(resolve));

test('concurrent txns: gap-free LSNs, intact chains, correct recovery', async () => {
  const sys = boot({ seed: 71 });
  const { tm } = sys;
  const rng = mulberry32(1234);
  const workerCount = 6;
  const opsPerWorker = 15;

  const tasks: Promise<void>[] = [];
  for (let w = 0; w < workerCount; w++) {
    tasks.push(
      (async () => {
        const t = await tm.begin();
        for (let i = 0; i < opsPerWorker; i++) {
          await tm.set(t, 1 + (i % 3), `w${w}k${i}`, w * 100 + i);
          if (rng() < 0.5) await yieldCtl(); // force interleavings
        }
        if (w % 2 === 0) await tm.commit(t);
        else await tm.abort(t);
      })(),
    );
  }
  await Promise.all(tasks);

  // WAL integrity: LSNs are unique, contiguous and ordered.
  const all = sys.wal.allRecords();
  assert.deepEqual(
    all.map((r) => r.lsn),
    all.map((_, i) => i + 1),
    'LSNs must be gap-free 1..N despite concurrency',
  );

  // Per-transaction prevLSN chains are intact.
  const byTxn = new Map<number, typeof all>();
  for (const rec of all) {
    if (rec.txnId === 0) continue;
    const list = byTxn.get(rec.txnId) ?? [];
    list.push(rec);
    byTxn.set(rec.txnId, list);
  }
  for (const [txnId, list] of byTxn) {
    assert.equal(list[0].type, 'BEGIN', `txn ${txnId} must start with BEGIN`);
    assert.equal(list[0].prevLSN, 0);
    for (let i = 1; i < list.length; i++) {
      assert.equal(list[i].prevLSN, list[i - 1].lsn, `txn ${txnId} chain broken at ${list[i].lsn}`);
    }
  }
  assert.deepEqual(tm.openTxnIds(), [], 'no txn leaks after commit/abort');

  // Crash with everything durable, then recover.
  await sys.wal.flush();
  const sys2 = boot(crash(sys));
  const report = await recover(sys2);
  assert.deepEqual(report.losers, [], 'all txns ended cleanly before the crash');

  // Committed workers (even w) persisted; aborted workers (odd w) vanished.
  const committed = committedTxnIds(sys2.wal.readDurable());
  assert.equal(committed.size, workerCount / 2);
  for (let w = 0; w < workerCount; w++) {
    for (let i = 0; i < opsPerWorker; i++) {
      const page = sys2.disk.readPage(1 + (i % 3))!;
      const key = `w${w}k${i}`;
      if (w % 2 === 0) assert.equal(page.data[key], w * 100 + i, `committed key ${key} lost`);
      else assert.ok(!(key in page.data), `aborted key ${key} leaked`);
    }
  }
  assertPageLSNConsistent(sys2);
  assertNoLeaks(sys2);
});

test('concurrent commits and aborts racing a checkpoint stay consistent', async () => {
  const sys = boot({ seed: 73 });
  const { tm } = sys;
  const rng = mulberry32(777);

  const t1 = await tm.begin();
  await tm.set(t1, 1, 'shared', 1);
  const workers: Promise<void>[] = [];
  for (let w = 0; w < 4; w++) {
    workers.push(
      (async () => {
        const t = await tm.begin();
        for (let i = 0; i < 8; i++) {
          await tm.set(t, 2, `k${w}_${i}`, i);
          if (rng() < 0.5) await yieldCtl();
        }
        if (w % 2 === 0) await tm.commit(t);
        else await tm.abort(t);
      })(),
    );
  }
  const ckpt = (async () => {
    await yieldCtl();
    await tm.checkpoint();
  })();
  await tm.commit(t1);
  await Promise.all([...workers, ckpt]);
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  const report = await recover(sys2);
  assert.deepEqual(report.losers, []);
  assert.deepEqual(sys2.disk.readPage(1)!.data, { shared: 1 });
  const page2 = sys2.disk.readPage(2)!.data;
  for (let w = 0; w < 4; w++) {
    for (let i = 0; i < 8; i++) {
      const key = `k${w}_${i}`;
      if (w % 2 === 0) assert.equal(page2[key], i, `committed key ${key} lost`);
      else assert.ok(!(key in page2), `aborted key ${key} leaked`);
    }
  }
  assertPageLSNConsistent(sys2);
  assertNoLeaks(sys2);
});
