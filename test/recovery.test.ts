import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LogManager } from '../src/log.ts';
import { BufferPool, Disk } from '../src/storage.ts';
import { TransactionManager } from '../src/txn.ts';
import { CrashInjected, RecoveryManager } from '../src/recovery.ts';
import { RecordType } from '../src/types.ts';
import { buildAcceptanceScenario, EXPECTED_DISK } from './helpers.ts';

function assertDiskMatches(disk: Disk, expected: [number, number][]) {
  for (const [page, value] of expected) {
    assert.equal(disk.readPage(page).value, value, `page ${page}`);
  }
}

test('acceptance: 3 uncommitted + 2 committed txns, crash after checkpoint', () => {
  const s = buildAcceptanceScenario();
  const rm = new RecoveryManager(s.log, s.disk, s.buffer);
  const res = rm.recover();

  assertDiskMatches(s.disk, EXPECTED_DISK);

  assert.equal(res.undo.clrCount, s.loserUpdateCount, 'one CLR per loser update');
  for (const t of s.losers) assert.ok(res.undo.endedTxns.includes(t), `txn ${t} ended`);
  assert.deepEqual([...res.undo.endedTxns].sort(), [...s.losers].sort(), 'only losers need END');

  const stable = s.log.stableRecords();
  const clrs = stable.filter((r) => r.type === RecordType.CLR);
  assert.equal(clrs.length, s.loserUpdateCount);
  for (const clr of clrs) {
    assert.ok(s.losers.includes(clr.txnId), 'CLRs only for loser txns');
    assert.notEqual(clr.undoNextLSN, undefined);
  }
  const ends = stable.filter((r) => r.type === RecordType.END).map((r) => r.txnId);
  for (const t of [...s.losers, s.winners[0], s.winners[1]]) assert.ok(ends.includes(t));

  assert.equal(s.log.bufferedCount, 0, 'no unflushed log records after recovery');
  assert.equal(s.buffer.dirtyCount, 0, 'no dirty frames after recovery');
});

test('redo repeats history including loser updates before undo', () => {
  const s = buildAcceptanceScenario();
  const rm = new RecoveryManager(s.log, s.disk, s.buffer);
  const analysis = rm.analyze();
  const redoStats = rm.redo(analysis);
  assert.equal(redoStats.redone, 6, 'all 6 post-checkpoint updates redone (incl. loser updates)');
  const frame1 = s.buffer.fix(1);
  assert.equal(frame1.value, 101, 'loser update visible after redo (repeat history)');
});

test('recovery is idempotent: second recovery is a no-op', () => {
  const s = buildAcceptanceScenario();
  new RecoveryManager(s.log, s.disk, s.buffer).recover();
  const before = s.disk.snapshot();
  const res2 = new RecoveryManager(s.log, s.disk, s.buffer).recover();
  assert.equal(res2.analysis.txnTable.size, 0, 'no open txns after first recovery');
  assert.equal(res2.undo.clrCount, 0, 'no new CLRs on re-recovery');
  assert.equal(res2.redo.redone, 0, 'disk pageLSNs already current');
  assert.deepEqual(s.disk.snapshot(), before);
});

test('crash during undo: re-recovery completes via CLR undoNextLSN', () => {
  const s = buildAcceptanceScenario();
  const rm1 = new RecoveryManager(s.log, s.disk, s.buffer);
  assert.throws(() => rm1.recover({ crashAfterCLRs: 2 }), CrashInjected);

  const rm2 = new RecoveryManager(s.log, s.disk, s.buffer);
  const res = rm2.recover();
  assertDiskMatches(s.disk, EXPECTED_DISK);
  assert.ok(res.undo.clrCount > 0);

  const stable = s.log.stableRecords();
  const loserUpdates = stable.filter(
    (r) => r.type === RecordType.UPDATE && s.losers.includes(r.txnId),
  );
  const loserClrs = stable.filter((r) => r.type === RecordType.CLR && s.losers.includes(r.txnId));
  assert.equal(loserClrs.length, loserUpdates.length, 'each loser update compensated exactly once');
});

test('undo resumes for txn in ABORTING state at crash (partial rollback)', () => {
  const log = new LogManager();
  const disk = new Disk();
  const buffer = new BufferPool(disk);
  const tm = new TransactionManager(log, buffer);

  const t1 = tm.begin();
  tm.update(t1, 1, 10);
  tm.update(t1, 1, 20);
  const lastLSN = tm.txnTable.get(t1)!.lastLSN;
  const firstUpdateLSN = lastLSN - 1;

  log.append({ type: RecordType.ABORT, txnId: t1, prevLSN: lastLSN });
  log.append({
    type: RecordType.CLR,
    txnId: t1,
    prevLSN: lastLSN + 1,
    pageId: 1,
    before: 20,
    after: 10,
    undoNextLSN: firstUpdateLSN,
  });
  log.force();
  buffer.crash();
  log.crash();

  const rm = new RecoveryManager(log, disk, buffer);
  const res = rm.recover();
  assert.equal(disk.readPage(1).value, 0, 'both updates undone, second one not undone twice');
  assert.equal(res.undo.clrCount, 1, 'only the remaining first update gets a new CLR');
});

test('boundary: empty log recovery', () => {
  const log = new LogManager();
  const disk = new Disk();
  const buffer = new BufferPool(disk);
  const res = new RecoveryManager(log, disk, buffer).recover();
  assert.equal(res.undo.clrCount, 0);
  assert.equal(res.redo.redone, 0);
});

test('boundary: checkpoint-only log', () => {
  const log = new LogManager();
  const disk = new Disk();
  const buffer = new BufferPool(disk);
  const tm = new TransactionManager(log, buffer);
  tm.checkpoint();
  buffer.crash();
  log.crash();
  const res = new RecoveryManager(log, disk, buffer).recover();
  assert.equal(res.analysis.txnTable.size, 0);
  assert.equal(res.undo.clrCount, 0);
});

test('boundary: all txns committed, crash with unflushed dirty pages', () => {
  const log = new LogManager();
  const disk = new Disk();
  const buffer = new BufferPool(disk);
  const tm = new TransactionManager(log, buffer);
  const t1 = tm.begin();
  tm.update(t1, 1, 5);
  tm.commit(t1);
  const t2 = tm.begin();
  tm.update(t2, 2, 6);
  tm.commit(t2);
  buffer.crash();
  log.crash();
  const res = new RecoveryManager(log, disk, buffer).recover();
  assert.equal(res.undo.clrCount, 0, 'no losers');
  assert.equal(disk.readPage(1).value, 5);
  assert.equal(disk.readPage(2).value, 6);
});

test('boundary: crash immediately after checkpoint with no further activity', () => {
  const log = new LogManager();
  const disk = new Disk();
  const buffer = new BufferPool(disk);
  const tm = new TransactionManager(log, buffer);
  const t1 = tm.begin();
  tm.update(t1, 1, 42);
  tm.commit(t1);
  tm.checkpoint();
  buffer.crash();
  log.crash();
  const res = new RecoveryManager(log, disk, buffer).recover();
  assert.equal(res.redo.redone, 0, 'checkpoint flushed everything');
  assert.equal(disk.readPage(1).value, 42);
});
