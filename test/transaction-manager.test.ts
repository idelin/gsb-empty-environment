import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WalManager } from '../src/wal.ts';
import { BufferPool, Disk, LatchManager } from '../src/storage.ts';
import { TransactionManager } from '../src/transaction-manager.ts';
import { LogRecordType } from '../src/log-record.ts';
import type { ClrRecord, UpdateRecord } from '../src/log-record.ts';

function setup() {
  const wal = new WalManager();
  const disk = new Disk();
  const pool = new BufferPool(disk, wal);
  const latches = new LatchManager();
  const tm = new TransactionManager(wal, pool, latches);
  return { wal, disk, pool, latches, tm };
}

test('commit writes COMMIT+END, flushes log to commit LSN, frees the txn', () => {
  const { wal, tm } = setup();
  const t = tm.begin();
  const upd = tm.update(t, 'P1', 'k', 42);
  tm.commit(t);
  const types = wal.all().map((r) => r.type);
  assert.deepEqual(types, [LogRecordType.UPDATE, LogRecordType.COMMIT, LogRecordType.END]);
  assert.ok(wal.getFlushedLSN() >= upd + 1, 'log durable at least to COMMIT');
  assert.equal(tm.activeTxns().size, 0);
  assert.equal(tm.latchLeakCount(), 0);
  assert.throws(() => tm.update(t, 'P1', 'k', 1), /unknown transaction/);
});

test('operations on non-running transactions are rejected', () => {
  const { tm } = setup();
  const t = tm.begin();
  tm.commit(t);
  assert.throws(() => tm.commit(t), /unknown transaction/);
  const t2 = tm.begin();
  tm.abort(t2);
  assert.throws(() => tm.update(t2, 'P', 'k', 1), /unknown transaction/);
});

test('abort rolls back in reverse order with correctly chained CLRs', () => {
  const { wal, pool, tm } = setup();
  const t = tm.begin();
  tm.update(t, 'P1', 'k', 1); // 1
  tm.update(t, 'P1', 'k', 2); // 2
  tm.update(t, 'P2', 'j', 9); // 3
  tm.abort(t); // 4=ABORT, 5..7=CLRs, 8=END

  const log = wal.all();
  assert.equal(log[3]!.type, LogRecordType.ABORT);
  const clrs = log.filter((r) => r.type === LogRecordType.CLR) as ClrRecord[];
  assert.equal(clrs.length, 3);
  // Reverse order: undo LSN 3, then 2, then 1.
  assert.deepEqual(clrs.map((c) => c.undoneLSN), [3, 2, 1]);
  // undoNextLSN of each CLR = prevLSN of the record it undoes.
  const updates = log.filter((r) => r.type === LogRecordType.UPDATE) as UpdateRecord[];
  for (const clr of clrs) {
    const undone = updates.find((u) => u.lsn === clr.undoneLSN)!;
    assert.equal(clr.undoNextLSN, undone.prevLSN);
  }
  assert.equal(log.at(-1)!.type, LogRecordType.END);
  // Before-images restored.
  assert.equal(pool.fetch('P1').data.k, undefined);
  assert.equal('k' in pool.fetch('P1').data, false);
  assert.equal(pool.fetch('P2').data.j, undefined);
  assert.equal(tm.activeTxns().size, 0);
  assert.equal(tm.latchLeakCount(), 0);
});

test('abort restores intermediate before-images for repeated updates', () => {
  const { pool, tm } = setup();
  const t1 = tm.begin();
  tm.update(t1, 'P1', 'k', 'committed-value');
  tm.commit(t1);
  const t2 = tm.begin();
  tm.update(t2, 'P1', 'k', 'temp-1');
  tm.update(t2, 'P1', 'k', 'temp-2');
  tm.abort(t2);
  assert.equal(pool.fetch('P1').data.k, 'committed-value');
});

test('checkpoint snapshots the transaction table and dirty page table', () => {
  const { wal, tm } = setup();
  const t1 = tm.begin();
  const t2 = tm.begin();
  tm.update(t1, 'P1', 'a', 1); // 1
  tm.update(t2, 'P2', 'b', 2); // 2
  tm.update(t1, 'P2', 'c', 3); // 3 (P2 already dirty: recLSN stays 2)
  const ckptLSN = tm.checkpoint(); // 4
  const ckpt = wal.get(ckptLSN);
  assert.equal(ckpt.type, LogRecordType.CHECKPOINT);
  if (ckpt.type !== LogRecordType.CHECKPOINT) return;
  assert.deepEqual(
    ckpt.txnTable.map((e) => [e.txnId, e.status, e.lastLSN]),
    [
      [t1, 'RUNNING', 3],
      [t2, 'RUNNING', 2],
    ],
  );
  assert.deepEqual(ckpt.dirtyPageTable, { P1: 1, P2: 2 });
  assert.ok(wal.getFlushedLSN() >= ckptLSN);
});

test('strict 2PL: conflicting key update is rejected until the holder ends', () => {
  const { pool, latches, tm } = setup();
  const t1 = tm.begin();
  const t2 = tm.begin();
  tm.update(t1, 'P1', 'k', 't1');
  assert.throws(() => tm.update(t2, 'P1', 'k', 't2'), /lock conflict/);
  // Non-conflicting keys/pages are fine.
  tm.update(t2, 'P1', 'j', 't2-j');
  tm.update(t2, 'P2', 'k', 't2');
  tm.commit(t1); // releases t1's locks
  tm.update(t2, 'P1', 'k', 't2-now-allowed');
  tm.commit(t2);
  assert.equal(pool.fetch('P1').data.k, 't2-now-allowed');
  assert.equal(latches.heldCount, 0);
  assert.equal(tm.lockLeakCount(), 0);
});

test('2PL locks are re-entrant per txn and released on abort', () => {
  const { tm } = setup();
  const t1 = tm.begin();
  const t2 = tm.begin();
  tm.update(t1, 'P1', 'k', 1);
  tm.update(t1, 'P1', 'k', 2); // same txn re-acquires: fine
  assert.throws(() => tm.update(t2, 'P1', 'k', 3), /lock conflict/);
  tm.abort(t1);
  assert.equal(tm.lockLeakCount(), 0);
  tm.update(t2, 'P1', 'k', 3); // lock free after abort
  tm.commit(t2);
  assert.equal(tm.lockLeakCount(), 0);
});
