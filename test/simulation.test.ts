import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Simulator, reproduce } from '../src/simulator.ts';
import { LogManager, MemoryWALStorage } from '../src/wal.ts';
import { BufferPool, Disk } from '../src/storage.ts';
import { TransactionManager } from '../src/txn.ts';
import { RecoveryManager } from '../src/recovery.ts';

function assertCleanRun(result: ReturnType<typeof reproduce>): void {
  assert.deepEqual(result.leaks, [], `leaks: ${result.leaks.join('; ')}`);
  assert.equal(result.verification.ok, true);
}

test('determinism: same seed produces bit-identical WAL and disk state', () => {
  const a = reproduce(42, 20_000);
  const b = reproduce(42, 20_000);
  assert.equal(a.walHash, b.walHash);
  assert.equal(a.diskHash, b.diskHash);
  assert.equal(a.walLength, b.walLength);
  assertCleanRun(a);
});

test('determinism: different seeds diverge', () => {
  const a = reproduce(1, 5_000);
  const b = reproduce(2, 5_000);
  assert.notEqual(a.walHash, b.walHash);
});

test('fault reproduction: (seed, events, crashAt) replays the exact same crash and recovery', () => {
  const original = new Simulator({ seed: 99, crashAt: 15_000 }).run(50_000);
  const replay = reproduce(99, 50_000, 15_000);
  assert.equal(original.crashedAt, 15_000);
  assert.equal(original.walHash, replay.walHash);
  assert.equal(original.diskHash, replay.diskHash);
  assert.deepEqual(original.losers, replay.losers);
  assertCleanRun(original);
});

test('crash window: every crash point in a sweep recovers cleanly', () => {
  for (const crashAt of [100, 500, 1_000, 2_500, 5_000, 7_777]) {
    const result = reproduce(1234, 10_000, crashAt);
    assertCleanRun(result);
  }
});

test('contention: 32 concurrent txns on 4 pages x 2 keys stays correct (strict 2PL)', () => {
  const result = new Simulator({
    seed: 555,
    maxActiveTxns: 32,
    numPages: 4,
    keysPerPage: 2,
    bufferCapacity: 2,
    checkpointEvery: 200,
  }).run(50_000);
  assertCleanRun(result);
  const again = new Simulator({
    seed: 555,
    maxActiveTxns: 32,
    numPages: 4,
    keysPerPage: 2,
    bufferCapacity: 2,
    checkpointEvery: 200,
  }).run(50_000);
  assert.equal(result.walHash, again.walHash);
});

test('durability: committed txn survives crash even when its pages were never flushed', () => {
  const storage = new MemoryWALStorage();
  const disk = new Disk();
  const log = new LogManager(storage);
  const bp = new BufferPool(disk, 8, log);
  const tm = new TransactionManager(log, bp);
  const t1 = tm.begin();
  tm.update(t1, 0, 'k', 777);
  tm.commit(t1); // COMMIT durable; page stays only in the volatile buffer pool
  log.crash();
  bp.discardAll();

  const rlog = new LogManager(storage);
  const rbp = new BufferPool(disk, 8, rlog);
  const report = new RecoveryManager(rlog, rbp).recover();
  assert.equal(report.redoApplied, 1);
  assert.equal(disk.readPage(0).data['k'], 777);
});

test('steal: uncommitted data flushed to disk before crash is rolled back', () => {
  const storage = new MemoryWALStorage();
  const disk = new Disk();
  const log = new LogManager(storage);
  const bp = new BufferPool(disk, 8, log);
  const tm = new TransactionManager(log, bp);
  const t1 = tm.begin();
  tm.update(t1, 0, 'k', 111);
  tm.commit(t1);
  bp.flushPage(0);
  const t2 = tm.begin();
  tm.update(t2, 0, 'k', 222); // uncommitted
  bp.flushPage(0); // steal: uncommitted 222 reaches disk
  log.flush();
  log.crash();
  bp.discardAll();
  assert.equal(disk.readPage(0).data['k'], 222);

  const rlog = new LogManager(storage);
  const rbp = new BufferPool(disk, 8, rlog);
  const report = new RecoveryManager(rlog, rbp).recover();
  assert.deepEqual(report.losers, [t2]);
  assert.equal(disk.readPage(0).data['k'], 111);
});

test('million-step regression: 1,000,000 deterministic events, golden hashes', { timeout: 120_000 }, () => {
  const result = reproduce(7, 1_000_000);
  assert.equal(result.events, 1_000_000);
  assert.equal(result.walHash, '6e3cad08f8f3e9bf');
  assert.equal(result.diskHash, 'f0eb6c202a3ddcaf1d10adc01b2dd79e2917526a32b486edf0994ccf4e9f0bfa');
  assertCleanRun(result);
  // Spot-check scale: real work happened.
  assert.equal(result.committedTxns > 100_000, true);
  assert.equal(result.walLength > 1_000_000, true);
});
