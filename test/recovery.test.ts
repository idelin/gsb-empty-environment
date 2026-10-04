import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join } from 'node:path';
import { readdirSync } from 'node:fs';
import { LogType } from '../src/types.ts';
import { WriteAheadLog } from '../src/wal.ts';
import { RecoveryManager, SimulatedCrashError } from '../src/recovery.ts';
import { makeEnv, reopenAfterCrash } from './helpers.ts';
import type { TestEnv } from './helpers.ts';

/**
 * Builds the acceptance workload:
 *   T1, T2 committed; T3, T4, T5 uncommitted; a checkpoint in the middle;
 *   then a crash (some dirty pages stolen to disk, rest lost).
 * Returns the LSNs needed for precise assertions.
 */
function buildCrashWorkload(env: TestEnv): {
  lsns: Record<string, number>;
  checkpointLSN: number;
} {
  const { txns, checkpoints, pool } = env;
  const lsns: Record<string, number> = {};

  const t1 = txns.begin();
  lsns.t1u1 = txns.update(t1, 1, 'A1');
  lsns.t1u2 = txns.update(t1, 2, 'B1');

  const t2 = txns.begin();
  lsns.t2u1 = txns.update(t2, 3, 'C1');

  const checkpointLSN = checkpoints.checkpoint();

  const t3 = txns.begin();
  lsns.t3u1 = txns.update(t3, 1, 'A3');

  lsns.t1u3 = txns.update(t1, 2, 'B1x');
  txns.commit(t1);

  lsns.t2u2 = txns.update(t2, 3, 'C2');
  txns.commit(t2);

  const t4 = txns.begin();
  lsns.t4u1 = txns.update(t4, 4, 'D4');

  const t5 = txns.begin();
  lsns.t5u1 = txns.update(t5, 5, 'E5');
  lsns.t5u2 = txns.update(t5, 1, 'A5');

  // Steal two dirty pages to disk before the crash: page 2 holds committed
  // data, page 1 holds UNCOMMITTED data (T5's A5). Pages 3/4/5 are lost.
  pool.flushPage(2);
  pool.flushPage(1);
  // CRASH: no END for T3/T4/T5, remaining dirty pages never flushed.
  return { lsns, checkpointLSN };
}

test('acceptance: 3 uncommitted + 2 committed txns, checkpoint, crash, 3-phase recovery', () => {
  const env = makeEnv();
  try {
    const { lsns, checkpointLSN } = buildCrashWorkload(env);
    const { wal, storage, pool, recovery } = reopenAfterCrash(env);

    const report = recovery.recover();

    // ---- Analysis ----
    assert.equal(report.analysis.checkpointLSN, checkpointLSN);
    const tt = new Map(report.analysis.txnTable.map((e) => [e.txnId, e]));
    assert.deepEqual([...tt.keys()].sort((a, b) => a - b), [3, 4, 5]);
    assert.equal(tt.get(3)!.status, 'RUNNING');
    assert.equal(tt.get(4)!.status, 'RUNNING');
    assert.equal(tt.get(5)!.status, 'RUNNING');
    assert.equal(tt.get(5)!.lastLSN, lsns.t5u2);
    const dpt = new Map(report.analysis.dirtyPages.map((d) => [d.pageId, d.recLSN]));
    assert.deepEqual([...dpt.keys()].sort((a, b) => a - b), [1, 2, 3, 4, 5]);
    assert.equal(dpt.get(1), lsns.t1u1); // recLSN survives from checkpoint
    assert.equal(dpt.get(2), lsns.t1u2);
    assert.equal(dpt.get(3), lsns.t2u1);
    assert.equal(dpt.get(4), lsns.t4u1); // first dirtied after checkpoint
    assert.equal(dpt.get(5), lsns.t5u1);
    assert.equal(report.analysis.redoLSN, lsns.t1u1);

    // ---- Redo repeats history (committed AND uncommitted) ----
    assert.ok(report.redo.applied.includes(lsns.t2u1)); // page 3 was lost
    assert.ok(report.redo.applied.includes(lsns.t2u2));
    assert.ok(report.redo.applied.includes(lsns.t4u1));
    assert.ok(report.redo.applied.includes(lsns.t5u1));
    assert.ok(report.redo.skipped.includes(lsns.t1u1)); // page 1 already newer on disk
    assert.ok(report.redo.skipped.includes(lsns.t1u3)); // page 2 already flushed

    // ---- Undo rolls back losers with CLRs ----
    assert.deepEqual(report.undo.losers, [3, 4, 5]);
    assert.equal(report.undo.clrs.length, 4); // T3:1, T4:1, T5:2 updates

    // ---- Final persisted state ----
    assert.equal(storage.readPage(1)!.data, 'A1'); // T1 committed, T3/T5 undone
    assert.equal(storage.readPage(2)!.data, 'B1x'); // T1 committed
    assert.equal(storage.readPage(3)!.data, 'C2'); // T2 committed, redone
    assert.equal(storage.readPage(4)!.data, ''); // T4 undone
    assert.equal(storage.readPage(5)!.data, ''); // T5 undone

    // ---- WAL now contains CLRs and END records for every loser ----
    const records = WriteAheadLog.read(wal.filePath);
    const clrs = records.filter((r) => r.type === LogType.CLR);
    assert.equal(clrs.length, 4);
    const ends = records.filter((r) => r.type === LogType.END);
    assert.deepEqual(
      ends.map((r) => r.txnId).sort((a, b) => (a ?? 0) - (b ?? 0)),
      [1, 2, 3, 4, 5],
    );

    // ---- No leaks: log forced, pool clean, no temp files ----
    assert.equal(wal.bufferedCount, 0);
    assert.equal(pool.dirtyCount, 0);
    assert.deepEqual(
      readdirSync(join(env.root, 'data')).filter((f) => f.endsWith('.tmp')),
      [],
    );
  } finally {
    env.cleanup();
  }
});

test('analysis without any checkpoint scans the whole log', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 7, 'x');
    env.txns.commit(t1);
    const t2 = env.txns.begin();
    env.txns.update(t2, 8, 'y');
    env.wal.flush();

    const { recovery } = reopenAfterCrash(env);
    const report = recovery.recover();
    assert.equal(report.analysis.checkpointLSN, null);
    assert.deepEqual(
      report.analysis.txnTable.map((e) => e.txnId),
      [2],
    );
    // page 7 (committed, never flushed) and page 8 (loser) are both dirty
    assert.deepEqual(report.analysis.dirtyPages, [
      { pageId: 7, recLSN: 2 },
      { pageId: 8, recLSN: 6 },
    ]);
    // recovery redid the committed page and rolled the loser back
    const { storage } = reopenAfterCrash(env);
    assert.equal(storage.readPage(7)!.data, 'x');
    assert.equal(storage.readPage(8)!.data, '');
  } finally {
    env.cleanup();
  }
});

test('redo is pageLSN-guarded: never re-applies a record already on disk', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'v1');
    env.txns.commit(t1);
    env.pool.flushAll();

    const { recovery, pool } = reopenAfterCrash(env);
    const report = recovery.recover();
    assert.deepEqual(report.redo.applied, []);
    assert.equal(pool.get(1).data, 'v1');
  } finally {
    env.cleanup();
  }
});

test('crash during undo: recovery is restartable and idempotent via CLRs', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'committed');
    env.txns.commit(t1);
    const t2 = env.txns.begin();
    env.txns.update(t2, 1, 'loser-1');
    env.txns.update(t2, 2, 'loser-2');
    env.txns.update(t2, 3, 'loser-3');
    env.wal.flush();

    // First recovery attempt crashes after writing 2 CLRs.
    const attempt1 = reopenAfterCrash(env);
    attempt1.recovery.crashAfterClrs = 2;
    assert.throws(() => attempt1.recovery.recover(), SimulatedCrashError);
    const clrsAfterCrash = WriteAheadLog.read(attempt1.wal.filePath).filter(
      (r) => r.type === LogType.CLR,
    );
    assert.equal(clrsAfterCrash.length, 2);

    // Second recovery completes; undo continues from the CLR chain.
    const attempt2 = reopenAfterCrash(env);
    const report = attempt2.recovery.recover();
    assert.equal(report.undo.clrs.length, 1); // only the remaining update
    assert.equal(attempt2.storage.readPage(1)!.data, 'committed');
    assert.equal(attempt2.storage.readPage(2)!.data, '');
    assert.equal(attempt2.storage.readPage(3)!.data, '');

    // Third recovery is a no-op: nothing left to undo, nothing to redo.
    const attempt3 = reopenAfterCrash(env);
    const report3 = attempt3.recovery.recover();
    assert.deepEqual(report3.analysis.txnTable, []);
    assert.equal(report3.undo.clrs.length, 0);
    assert.equal(attempt3.storage.readPage(1)!.data, 'committed');
  } finally {
    env.cleanup();
  }
});

test('txn in COMMITTING state at crash (commit flushed, END lost) is ended, not undone', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'kept');
    // Simulate a crash window: COMMIT forced, END not yet written.
    const entry = { commitLsn: 0 };
    const rec = env.wal.append({ type: LogType.COMMIT, txnId: t1, prevLSN: env.wal.currentLSN });
    entry.commitLsn = rec.lsn;
    env.wal.flush();
    env.pool.flushPage(1);

    const { recovery, storage } = reopenAfterCrash(env);
    const report = recovery.recover();
    const tt = report.analysis.txnTable;
    assert.equal(tt.length, 1);
    assert.equal(tt[0].status, 'COMMITTING');
    assert.deepEqual(report.undo.losers, []); // not treated as a loser
    assert.deepEqual(report.undo.ended, [t1]);
    assert.equal(storage.readPage(1)!.data, 'kept');
  } finally {
    env.cleanup();
  }
});

test('aborted-before-crash transactions leave no trace of their updates', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'doomed');
    env.txns.update(t1, 2, 'doomed-2');
    env.txns.abort(t1); // rollback writes CLRs immediately
    assert.equal(env.pool.get(1).data, '');
    env.pool.flushAll();

    const { recovery, storage } = reopenAfterCrash(env);
    const report = recovery.recover();
    assert.deepEqual(report.analysis.txnTable, []); // END was logged
    assert.equal(storage.readPage(1)!.data, '');
    assert.equal(storage.readPage(2)!.data, '');
  } finally {
    env.cleanup();
  }
});
