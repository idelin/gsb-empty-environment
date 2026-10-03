import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSystem } from '../src/system.ts';
import { FaultInjector } from '../src/fault.ts';
import { DiskFullError, SimulatedCrashError } from '../src/errors.ts';
import { LossyChannel, Replica } from '../src/replication.ts';
import { LogManager } from '../src/logManager.ts';
import { BufferPool } from '../src/bufferPool.ts';
import { recover } from '../src/recovery.ts';
import { LogType } from '../src/types.ts';
import {
  buildAcceptanceScenario,
  ACCEPTANCE_EXPECTED_PAGES,
  assertNoLeaks,
  assertLogIntegrity,
} from './helpers.ts';

/* ------------------------- power loss ------------------------- */

test('power loss: unflushed commit is lost and the txn is rolled back', () => {
  const system = new DatabaseSystem();
  const faults = new FaultInjector(system);
  const t1 = system.begin();
  system.update(t1, 1, 100); // LSN 1
  system.commit(t1);         // LSN 2,3 durable

  const t2 = system.begin();
  system.update(t2, 2, 200); // LSN 4
  system.logManager.flushAll(); // T2's UPDATE is durable...
  // Crash exactly when T2's COMMIT (LSN 5) is appended, before any flush.
  faults.crashAtLSN(5, { flushBeforeCrash: false });
  assert.throws(() => system.commit(t2), SimulatedCrashError);

  const report = system.recover();
  assert.deepEqual(report.undo.losers, [t2], 'lost commit => T2 is a loser');
  assert.equal(system.disk.pageValue(1), 100, 'T1 (committed) persists');
  assert.equal(system.disk.pageValue(2), 0, 'T2 rolled back');
  assertNoLeaks(system, report);
  assertLogIntegrity(system);
});

test('power loss: flushed commit without END is a winner and gets closed', () => {
  const system = new DatabaseSystem();
  const faults = new FaultInjector(system);
  const t1 = system.begin();
  system.update(t1, 1, 42); // LSN 1
  // Crash when the END record (LSN 3) is appended; COMMIT (LSN 2) is durable.
  faults.crashAtLSN(3, { flushBeforeCrash: false });
  assert.throws(() => system.commit(t1), SimulatedCrashError);

  const report = system.recover();
  assert.deepEqual(report.undo.winners, [t1]);
  assert.deepEqual(report.undo.losers, []);
  assert.equal(report.undo.clrCount, 0);
  assert.equal(system.disk.pageValue(1), 42, 'committed txn survives');
  assertNoLeaks(system, report);
  assertLogIntegrity(system);
});

test('power loss: torn tail write is truncated by checksum validation', () => {
  const { system, faults } = buildAcceptanceScenario();
  system.disk.corruptTail(); // torn write on the last durable record (LSN 13)
  faults.powerLoss();
  const report = system.recover();

  assert.ok(system.disk.stats.truncatedRecords > 0, 'corrupt tail was truncated');
  // T5's P5 update (LSN 13) is gone; P4 update (LSN 12) still rolled back.
  for (const [pageId, want] of ACCEPTANCE_EXPECTED_PAGES) {
    assert.equal(system.disk.pageValue(pageId), want, `page ${pageId}`);
  }
  assertNoLeaks(system, report);
  assertLogIntegrity(system);
});

test('power loss during undo: re-recovery never undoes the same update twice', () => {
  const { system, faults, t3, t4, t5 } = buildAcceptanceScenario();
  faults.powerLoss();

  // First recovery crashes right after the second CLR (LSN 15) is written.
  faults.crashAtLSN(15); // flushes CLRs 14,15 then kills the power
  assert.throws(() => system.recover(), SimulatedCrashError);
  assert.ok(system.isCrashed);

  const clrsAfterCrash = system.disk
    .readLog()
    .filter((r) => r.type === LogType.Clr).length;
  assert.equal(clrsAfterCrash, 2, 'two CLRs made it to disk before the crash');

  const report = system.recover();
  assert.equal(report.undo.clrCount, 2, 'only the remaining updates are compensated');
  assert.deepEqual(report.undo.losers.sort(), [t3, t4, t5]);

  const allClrs = system.disk.readLog().filter((r) => r.type === LogType.Clr);
  assert.equal(allClrs.length, 4, 'exactly one CLR per loser UPDATE — no double undo');
  for (const [pageId, want] of ACCEPTANCE_EXPECTED_PAGES) {
    assert.equal(system.disk.pageValue(pageId), want, `page ${pageId}`);
  }
  assertNoLeaks(system, report);
  assertLogIntegrity(system);
});

/* ------------------------- disk full -------------------------- */

test('disk full: commit fails cleanly and the retry succeeds without corruption', () => {
  const system = new DatabaseSystem();
  const faults = new FaultInjector(system);
  const t1 = system.begin();
  system.update(t1, 1, 7);

  faults.setDiskFull(true);
  assert.throws(() => system.commit(t1), DiskFullError);
  assert.equal(system.activeTxnCount, 1, 'txn survives the failed commit');

  faults.setDiskFull(false);
  system.commit(t1); // retry: no duplicate COMMIT record
  system.flushAllPages();

  const commits = system.disk.readLog().filter((r) => r.type === LogType.Commit);
  assert.equal(commits.length, 1, 'commit record appended exactly once');
  assert.equal(system.disk.pageValue(1), 7);
  assert.equal(system.activeTxnCount, 0);
  assertLogIntegrity(system);
});

test('disk full: page flush respects the WAL protocol and stays retryable', () => {
  const system = new DatabaseSystem();
  const faults = new FaultInjector(system);
  const t1 = system.begin();
  system.update(t1, 1, 11);

  faults.setDiskFull(true);
  assert.throws(() => system.bufferPool.flushPage(1), DiskFullError);
  assert.equal(system.bufferPool.dirtyCount, 1, 'page stays dirty after ENOSPC');
  assert.equal(system.disk.pageLSN(1), 0, 'nothing reached the disk');

  faults.setDiskFull(false);
  system.bufferPool.flushPage(1);
  // WAL protocol: the log record (LSN 1) is durable before the page is.
  assert.ok(system.logManager.getDurableLSN() >= system.disk.pageLSN(1));
  assert.equal(system.disk.pageValue(1), 11);
});

test('disk full during recovery: recovery aborts safely and heals on retry', () => {
  const { system, faults } = buildAcceptanceScenario();
  faults.powerLoss();

  faults.setDiskFull(true);
  assert.throws(() => system.recover(), DiskFullError);

  faults.setDiskFull(false);
  const report = system.recover(); // retry from scratch: idempotent
  for (const [pageId, want] of ACCEPTANCE_EXPECTED_PAGES) {
    assert.equal(system.disk.pageValue(pageId), want, `page ${pageId}`);
  }
  assertNoLeaks(system, report);
  assertLogIntegrity(system);
});

test('disk full: aborting a committing txn is refused (commit already logged)', () => {
  const system = new DatabaseSystem();
  const faults = new FaultInjector(system);
  const t1 = system.begin();
  system.update(t1, 1, 5);
  faults.setDiskFull(true);
  assert.throws(() => system.commit(t1), DiskFullError);
  assert.throws(() => system.abort(t1), /cannot be aborted/);
  faults.setDiskFull(false);
  system.commit(t1);
  assert.equal(system.activeTxnCount, 0);
});

/* ------------------------ packet loss ------------------------- */

test('packet loss: replica truncates at the gap and recovers the contiguous prefix', () => {
  const primary = new DatabaseSystem();
  const t1 = primary.begin();
  primary.update(t1, 1, 10);
  primary.commit(t1);
  const t2 = primary.begin();
  primary.update(t2, 2, 20);
  primary.commit(t2);
  const wal = primary.disk.readLog();

  // Drop T2's UPDATE (LSN 4): COMMIT/END behind the gap are discarded too.
  const channel = new LossyChannel();
  channel.dropLSNs([4]);
  const replica = new Replica();
  replica.receive(channel.ship(wal));

  assert.ok(replica.hasGap);
  assert.equal(replica.contiguousLSN, 3);

  const lm = new LogManager(replica.disk);
  const bp = new BufferPool(replica.disk, lm);
  const report = recover(replica.disk, lm, bp);
  assert.equal(replica.disk.pageValue(1), 10, 'T1 healed on the replica');
  assert.equal(replica.disk.pageValue(2), 0, 'T2 lost with the dropped packet');
  assert.equal(report.finalTxnTable.size, 0);
});

test('packet loss: seeded random loss still yields a consistent prefix', () => {
  const primary = new DatabaseSystem();
  const txns = [primary.begin(), primary.begin(), primary.begin()];
  for (const [i, txn] of txns.entries()) {
    primary.update(txn, i + 1, (i + 1) * 100);
    primary.commit(txn);
  }
  primary.logManager.flushAll();
  const wal = primary.disk.readLog();

  const channel = new LossyChannel();
  channel.setLossRate(0.4, 1234);
  const replica = new Replica();
  replica.receive(channel.ship(wal));

  // The replica's durable log is exactly the contiguous prefix up to the
  // first lost record (or the full log if nothing was lost).
  const durable = replica.disk.readLog();
  const firstGap = wal.findIndex((rec) => rec.lsn === replica.contiguousLSN + 1);
  void firstGap;
  durable.forEach((rec, i) => assert.equal(rec.lsn, i + 1));

  const lm = new LogManager(replica.disk);
  const bp = new BufferPool(replica.disk, lm);
  const report = recover(replica.disk, lm, bp);
  assert.equal(report.finalTxnTable.size, 0, 'replica recovery leaves no losers behind');
  // Every page value on the replica is either fully committed or absent.
  for (const [i] of txns.entries()) {
    const value = replica.disk.pageValue(i + 1);
    assert.ok(value === 0 || value === (i + 1) * 100, `page ${i + 1} is consistent`);
  }
});
