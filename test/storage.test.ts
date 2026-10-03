import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WalManager } from '../src/wal.ts';
import { BufferPool, Disk, LatchManager } from '../src/storage.ts';
import { LogRecordType } from '../src/log-record.ts';

function setup() {
  const wal = new WalManager();
  const disk = new Disk();
  const pool = new BufferPool(disk, wal);
  return { wal, disk, pool };
}

test('WAL protocol: dirty page cannot flush before its log is durable', () => {
  const { wal, pool } = setup();
  const page = pool.fetch('P1');
  page.data.k = 'v';
  const rec = wal.append({ type: LogRecordType.UPDATE, prevLSN: 0, txnId: 1, pageId: 'P1', key: 'k', before: undefined, after: 'v' });
  pool.markDirty('P1', rec.lsn);
  assert.throws(() => pool.flushPage('P1'), /WAL violation/);
  wal.flush(rec.lsn);
  pool.flushPage('P1'); // now legal
  assert.equal(pool.dirtyPages().length, 0);
});

test('recLSN tracks the first dirtying update and clears on flush', () => {
  const { wal, pool } = setup();
  pool.fetch('P1');
  const r1 = wal.append({ type: LogRecordType.UPDATE, prevLSN: 0, txnId: 1, pageId: 'P1', key: 'a', before: 0, after: 1 });
  pool.markDirty('P1', r1.lsn);
  const r2 = wal.append({ type: LogRecordType.UPDATE, prevLSN: 1, txnId: 1, pageId: 'P1', key: 'b', before: 0, after: 2 });
  pool.markDirty('P1', r2.lsn);
  assert.equal(pool.dirtyPageTable().get('P1'), r1.lsn); // first, not latest
  wal.flush(2);
  pool.flushPage('P1');
  assert.equal(pool.dirtyPageTable().size, 0);
  // Re-dirtying starts a new recLSN epoch.
  const r3 = wal.append({ type: LogRecordType.UPDATE, prevLSN: 2, txnId: 1, pageId: 'P1', key: 'c', before: 0, after: 3 });
  pool.markDirty('P1', r3.lsn);
  assert.equal(pool.dirtyPageTable().get('P1'), r3.lsn);
});

test('crash loses the buffer pool but not the disk', () => {
  const { wal, disk, pool } = setup();
  const page = pool.fetch('P1');
  page.data.k = 'durable';
  const rec = wal.append({ type: LogRecordType.UPDATE, prevLSN: 0, txnId: 1, pageId: 'P1', key: 'k', before: undefined, after: 'durable' });
  pool.markDirty('P1', rec.lsn);
  wal.flush(1);
  pool.flushPage('P1');
  page.data.k = 'volatile';
  const rec2 = wal.append({ type: LogRecordType.UPDATE, prevLSN: 1, txnId: 1, pageId: 'P1', key: 'k', before: 'durable', after: 'volatile' });
  pool.markDirty('P1', rec2.lsn);
  pool.crash();
  assert.equal(pool.dirtyPages().length, 0);
  assert.equal(disk.readPage('P1').data.k, 'durable');
  assert.equal(disk.readPage('P1').pageLSN, 1);
});

test('disk reads return independent copies', () => {
  const { disk } = setup();
  disk.writePage({ pageId: 'P1', pageLSN: 1, data: { k: 'v' } });
  const a = disk.readPage('P1');
  a.data.k = 'mutated';
  assert.equal(disk.readPage('P1').data.k, 'v');
});

test('latch manager: conflicting acquire throws, mismatched release throws, no leaks', () => {
  const latches = new LatchManager();
  const ownerA = Symbol('a');
  const ownerB = Symbol('b');
  latches.acquire('P1', ownerA);
  assert.throws(() => latches.acquire('P1', ownerB), /already latched/);
  assert.throws(() => latches.release('P1', ownerB), /not latched by this owner/);
  latches.release('P1', ownerA);
  assert.equal(latches.heldCount, 0);
  latches.acquire('P1', ownerB); // free again after release
  latches.release('P1', ownerB);
  assert.equal(latches.heldCount, 0);
});
