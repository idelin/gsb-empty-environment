import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LogManager, MemoryWALStorage } from '../src/wal.ts';
import { BufferPool, Disk } from '../src/storage.ts';
import { TransactionManager } from '../src/txn.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { EventQueue } from '../src/simulator.ts';
import type { LogRecord } from '../src/types.ts';

interface Harness {
  storage: MemoryWALStorage;
  disk: Disk;
  log: LogManager;
  bp: BufferPool;
  tm: TransactionManager;
}

function newHarness(): Harness {
  const storage = new MemoryWALStorage();
  const disk = new Disk();
  const log = new LogManager(storage);
  const bp = new BufferPool(disk, 16, log);
  const tm = new TransactionManager(log, bp);
  return { storage, disk, log, bp, tm };
}

function crashAndRecover(h: Harness) {
  h.log.crash();
  h.bp.discardAll();
  const log = new LogManager(h.storage);
  const bp = new BufferPool(h.disk, 16, log);
  const report = new RecoveryManager(log, bp).recover();
  return { log, bp, report };
}

test('analysis: reconstructs TT and DPT from fuzzy checkpoint', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  h.tm.update(t1, 1, 'a', 10);
  h.tm.commit(t1);
  const t2 = h.tm.begin();
  h.tm.update(t2, 2, 'b', 20);
  const t3 = h.tm.begin();
  h.tm.update(t3, 3, 'c', 30);
  h.tm.checkpoint();
  const t4 = h.tm.begin();
  h.tm.update(t4, 2, 'b', 21);
  h.log.flush();
  h.log.crash();
  h.bp.discardAll();

  const log = new LogManager(h.storage);
  const bp = new BufferPool(h.disk, 16);
  const analysis = new RecoveryManager(log, bp).analysis();

  assert.equal(analysis.checkpointBeginLSN !== null, true);
  // T1 ended before checkpoint; T2,T3 from snapshot; T4 discovered by scan.
  assert.deepEqual(
    [...analysis.tt.keys()].sort((a, b) => a - b),
    [t2, t3, t4],
  );
  assert.equal(analysis.tt.get(t2)?.status, 'RUNNING');
  assert.equal(analysis.tt.get(t4)?.lastLSN, 7);
  // DPT: pages 1,2,3 dirty at checkpoint (never flushed) + page 2 again.
  assert.deepEqual([...analysis.dpt.keys()].sort((a, b) => a - b), [1, 2, 3]);
  assert.equal(analysis.dpt.get(1), 0);
  assert.equal(analysis.redoLSN, 0);
});

test('analysis: works without any checkpoint (scans from LSN 0)', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  h.tm.update(t1, 5, 'x', 1);
  h.log.flush();
  h.log.crash();
  h.bp.discardAll();

  const log = new LogManager(h.storage);
  const bp = new BufferPool(h.disk, 16);
  const analysis = new RecoveryManager(log, bp).analysis();
  assert.equal(analysis.checkpointBeginLSN, null);
  assert.equal(analysis.tt.size, 1);
  assert.equal(analysis.dpt.get(5), 0);
});

test('redo: repeats history for committed txn whose pages were never flushed (no-force)', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  h.tm.update(t1, 1, 'a', 100);
  h.tm.update(t1, 2, 'b', 200);
  h.tm.commit(t1);
  h.log.flush();
  h.log.crash();
  h.bp.discardAll(); // committed data only in WAL, not on disk

  const { report, bp } = crashAndRecover(h);
  assert.equal(report.redoApplied, 2);
  assert.equal(bp.fetch(1).data['a'], 100);
  assert.equal(bp.fetch(2).data['b'], 200);
  assert.equal(h.disk.readPage(1).data['a'], 100); // flushed after recovery
});

test('redo: skips updates already on disk (pageLSN >= lsn)', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  h.tm.update(t1, 1, 'a', 100);
  h.tm.commit(t1);
  h.bp.flushPage(1); // page reaches disk before crash
  const t2 = h.tm.begin();
  h.tm.update(t2, 1, 'a', 101); // not flushed
  h.log.flush();
  h.log.crash();
  h.bp.discardAll();

  const { report } = crashAndRecover(h);
  assert.equal(report.redoApplied, 1); // only the second update
  assert.equal(report.redoSkipped, 1);
  assert.equal(h.disk.readPage(1).data['a'], 100); // t2 undone
});

test('undo: writes CLR chain with undoNextLSN and END for each loser', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  const lsn1 = h.tm.update(t1, 1, 'a', 10);
  const lsn2 = h.tm.update(t1, 1, 'a', 20);
  h.log.flush();
  h.log.crash();
  h.bp.discardAll();

  const { report, log } = crashAndRecover(h);
  assert.deepEqual(report.losers, [t1]);
  assert.equal(report.clrsWritten, 2);
  assert.equal(report.endsWritten, 1);

  const wal = log.durable();
  const clrs = wal.filter((r) => r.type === 'CLR');
  assert.equal(clrs.length, 2);
  assert.equal(clrs[0].type, 'CLR');
  if (clrs[0].type === 'CLR' && clrs[1].type === 'CLR') {
    assert.equal(clrs[0].undoNextLSN, lsn1); // undoes lsn2, points at lsn1
    assert.equal(clrs[1].undoNextLSN, null); // undoes lsn1, txn done
    assert.equal(clrs[1].prevLSN, clrs[0].lsn); // chain via txn lastLSN
  }
  assert.equal(lsn2, lsn1 + 1);
  assert.equal(wal[wal.length - 1].type, 'END');
  assert.equal(h.disk.readPage(1).data['a'], 0);
});

test('undo: resumes partial abort via undoNextLSN without re-undoing (crash after CLRs, END lost)', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  h.tm.update(t1, 1, 'a', 10);
  h.tm.update(t1, 1, 'a', 20);
  h.tm.abort(t1); // writes ABORT + 2 CLRs + END into the volatile tail
  // Flush everything except the END record: crash loses the END.
  h.log.flush(4); // UPDATE,UPDATE,ABORT,CLR,CLR durable; END (lsn 5) lost
  h.log.crash();
  h.bp.discardAll();

  const { report, log } = crashAndRecover(h);
  assert.deepEqual(report.losers, [t1]);
  assert.equal(report.clrsWritten, 0); // nothing left to undo
  assert.equal(report.endsWritten, 1); // recovery closes the txn
  const wal = log.durable();
  assert.equal(wal.filter((r) => r.type === 'CLR').length, 2);
  assert.equal(wal[wal.length - 1].type, 'END');
  assert.equal(h.disk.readPage(1).data['a'], 0);
});

test('recovery: empty WAL is a no-op', () => {
  const h = newHarness();
  const { report } = crashAndRecover(h);
  assert.equal(report.analysis.tt.size, 0);
  assert.equal(report.analysis.dpt.size, 0);
  assert.equal(report.redoApplied, 0);
  assert.equal(report.clrsWritten, 0);
  assert.deepEqual(report.losers, []);
});

test('recovery: committed-only WAL needs no undo', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  h.tm.update(t1, 1, 'a', 1);
  h.tm.commit(t1);
  const t2 = h.tm.begin();
  h.tm.update(t2, 2, 'b', 2);
  h.tm.commit(t2);
  h.log.flush();
  const { report } = crashAndRecover(h);
  assert.deepEqual(report.losers, []);
  assert.equal(report.clrsWritten, 0);
  assert.equal(h.disk.readPage(1).data['a'], 1);
  assert.equal(h.disk.readPage(2).data['b'], 2);
});

test('recovery: loser with no log records still gets END (leak-free)', () => {
  const h = newHarness();
  const t1 = h.tm.begin(); // begins, does nothing
  h.tm.checkpoint();
  h.log.crash();
  h.bp.discardAll();

  const { report, log } = crashAndRecover(h);
  assert.deepEqual(report.losers, [t1]);
  assert.equal(report.clrsWritten, 0);
  assert.equal(report.endsWritten, 1);
  const wal = log.durable();
  assert.equal(wal[wal.length - 1].type, 'END');
});

test('recovery: committed txn with lost END record is a winner, not a loser', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  h.tm.update(t1, 1, 'a', 42);
  h.tm.commit(t1); // COMMIT flushed, END sits in volatile tail
  h.log.crash(); // END lost
  h.bp.discardAll();

  const { report } = crashAndRecover(h);
  assert.deepEqual(report.losers, []);
  assert.deepEqual(report.winners, [t1]);
  assert.equal(report.clrsWritten, 0);
  assert.equal(h.disk.readPage(1).data['a'], 42);
});

test('event queue: pops in deterministic (time, seq) order', () => {
  const q = new EventQueue();
  q.schedule(5, { kind: 'begin' });
  q.schedule(1, { kind: 'checkpoint' });
  q.schedule(5, { kind: 'steal' });
  q.schedule(1, { kind: 'begin' });
  const kinds: string[] = [];
  let event;
  while ((event = q.pop())) kinds.push(event.payload.kind);
  assert.deepEqual(kinds, ['checkpoint', 'begin', 'begin', 'steal']);
  assert.equal(q.size, 0);
});

test('wal file format: every record is one JSON line with monotonic LSNs', () => {
  const h = newHarness();
  const t1 = h.tm.begin();
  h.tm.update(t1, 1, 'a', 1);
  h.tm.commit(t1);
  h.log.flush();
  const wal: LogRecord[] = h.storage.readAll();
  wal.forEach((r, i) => assert.equal(r.lsn, i));
});
