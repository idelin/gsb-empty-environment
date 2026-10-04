import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LogType } from '../src/types.ts';
import type { EndCheckpointRecord } from '../src/types.ts';
import { WriteAheadLog } from '../src/wal.ts';
import { makeEnv, reopenAfterCrash } from './helpers.ts';

test('checkpoint writes BEGIN/END pair carrying txn table and dirty page table', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    const u1 = env.txns.update(t1, 1, 'a');
    const t2 = env.txns.begin();
    const u2 = env.txns.update(t2, 2, 'b');
    env.txns.commit(t2); // t2 ends before checkpoint
    const t3 = env.txns.begin();
    const u3 = env.txns.update(t3, 2, 'b2');

    const endLsn = env.checkpoints.checkpoint();
    const records = WriteAheadLog.read(env.wal.filePath);
    const begin = records.find((r) => r.type === LogType.BEGIN_CHECKPOINT);
    const end = records.find((r) => r.type === LogType.END_CHECKPOINT) as EndCheckpointRecord;
    assert.ok(begin, 'BEGIN_CHECKPOINT logged');
    assert.ok(end, 'END_CHECKPOINT logged');
    assert.ok(begin.lsn < end.lsn);
    assert.equal(end.lsn, endLsn);

    // Txn table: t1 and t3 active, t2 already ended.
    const tt = new Map(end.txnTable.map((e) => [e.txnId, e]));
    assert.deepEqual([...tt.keys()].sort((a, b) => a - b), [1, 3]);
    assert.equal(tt.get(1)!.status, 'RUNNING');
    assert.equal(tt.get(1)!.lastLSN, u1);
    assert.equal(tt.get(3)!.lastLSN, u3);

    // Dirty page table: page 1 dirty since u1, page 2 since u2 (first dirtying).
    const dpt = new Map(end.dirtyPages.map((d) => [d.pageId, d.recLSN]));
    assert.equal(dpt.get(1), u1);
    assert.equal(dpt.get(2), u2);
  } finally {
    env.cleanup();
  }
});

test('analysis starts from the MOST RECENT checkpoint, ignoring older history', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'old');
    env.checkpoints.checkpoint(); // checkpoint #1 mentions t1
    env.txns.commit(t1);
    env.pool.flushAll();

    const t2 = env.txns.begin();
    env.txns.update(t2, 2, 'new');
    env.checkpoints.checkpoint(); // checkpoint #2 mentions t2 only
    env.txns.commit(t2);

    const t3 = env.txns.begin();
    env.txns.update(t3, 3, 'post-ckpt');
    env.wal.flush();

    const { recovery } = reopenAfterCrash(env);
    const report = recovery.recover();
    // t1 must not appear: it ended before checkpoint #2.
    assert.deepEqual(
      report.analysis.txnTable.map((e) => e.txnId),
      [3],
    );
    // DPT: page 2 from checkpoint #2, page 3 from the trailing scan.
    // Page 1 was flushed and is absent.
    assert.deepEqual(
      report.analysis.dirtyPages.map((d) => d.pageId).sort((a, b) => a - b),
      [2, 3],
    );
  } finally {
    env.cleanup();
  }
});

test('checkpoint with no active transactions and no dirty pages', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'x');
    env.txns.commit(t1);
    env.pool.flushAll();

    env.checkpoints.checkpoint();
    const records = WriteAheadLog.read(env.wal.filePath);
    const end = records.find((r) => r.type === LogType.END_CHECKPOINT) as EndCheckpointRecord;
    assert.deepEqual(end.txnTable, []);
    assert.deepEqual(end.dirtyPages, []);

    const { recovery } = reopenAfterCrash(env);
    const report = recovery.recover();
    assert.deepEqual(report.analysis.txnTable, []);
    assert.deepEqual(report.analysis.dirtyPages, []);
    assert.deepEqual(report.redo.applied, []);
    assert.deepEqual(report.undo.losers, []);
  } finally {
    env.cleanup();
  }
});

test('checkpoint forces the WAL (records survive a subsequent crash)', () => {
  const env = makeEnv();
  try {
    const t1 = env.txns.begin();
    env.txns.update(t1, 1, 'durable');
    env.checkpoints.checkpoint();
    // Crash without any further flush: checkpoint itself forced the log.
    const { recovery, storage } = reopenAfterCrash(env);
    const report = recovery.recover();
    assert.deepEqual(
      report.analysis.txnTable.map((e) => e.txnId),
      [1],
    );
    assert.equal(storage.readPage(1)!.data, ''); // loser rolled back
  } finally {
    env.cleanup();
  }
});
