import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSystem } from '../src/system.ts';
import { analysisPhase, redoPhase, undoPhase, MaxHeap } from '../src/recovery.ts';
import { LogManager } from '../src/logManager.ts';
import { BufferPool } from '../src/bufferPool.ts';
import { LogType } from '../src/types.ts';
import type { ClrRecord } from '../src/types.ts';
import { buildAcceptanceScenario } from './helpers.ts';

function recoverThroughRedo(system: DatabaseSystem) {
  const lm = new LogManager(system.disk);
  const bp = new BufferPool(system.disk, lm);
  const log = lm.readAll();
  const analysis = analysisPhase(log);
  redoPhase(log, analysis, system.disk, bp);
  return { lm, bp, analysis };
}

test('max-heap pops in strict descending order', () => {
  const heap = new MaxHeap();
  for (const v of [5, 1, 9, 3, 7, 7, 2]) heap.push(v);
  const out: number[] = [];
  for (;;) {
    const v = heap.pop();
    if (v === undefined) break;
    out.push(v);
  }
  assert.deepEqual(out, [9, 7, 7, 5, 3, 2, 1]);
});

test('undo: rolls back every loser and writes a well-formed CLR chain', () => {
  const { system, t3, t4, t5 } = buildAcceptanceScenario();
  system.crash();
  const { lm, bp, analysis } = recoverThroughRedo(system);
  const stats = undoPhase(analysis, lm, bp);

  assert.deepEqual(stats.losers, [t3, t4, t5]);
  assert.equal(stats.clrCount, 4); // T3:1, T4:1, T5:2
  assert.deepEqual(stats.endedTxns, [t3, t4, t5]);

  // Values undone in the buffer pool.
  assert.equal(bp.read(1), 100);
  assert.equal(bp.read(2), 200);
  assert.equal(bp.read(4), 0);
  assert.equal(bp.read(5), 0);

  // CLR chain integrity: each CLR points back to the compensated UPDATE's prevLSN.
  const log = lm.readAll();
  const clrs = log.filter((r): r is ClrRecord => r.type === LogType.Clr);
  assert.equal(clrs.length, 4);
  for (const clr of clrs) {
    const compensated = log.find((r) => r.lsn === clr.prevLSN);
    assert.equal(compensated?.type, LogType.Update, 'CLR prevLSN references its UPDATE');
    if (compensated?.type === LogType.Update) {
      assert.equal(clr.undoNextLSN, compensated.prevLSN);
      assert.equal(clr.beforeImage, compensated.afterImage, 'CLR undoes the UPDATE after-image');
      assert.equal(clr.afterImage, compensated.beforeImage, 'CLR restores the UPDATE before-image');
      assert.equal(clr.pageId, compensated.pageId);
    }
  }
  // Every loser got an END record.
  for (const txnId of [t3, t4, t5]) {
    assert.ok(log.some((r) => r.type === LogType.End && 'txnId' in r && r.txnId === txnId));
  }
});

test('undo: processes multiple losers in global reverse LSN order', () => {
  const { system } = buildAcceptanceScenario();
  system.crash();
  const { lm, bp, analysis } = recoverThroughRedo(system);
  undoPhase(analysis, lm, bp);
  const clrs = lm.readAll().filter((r) => r.type === LogType.Clr);
  // Loser chains: T5@13->12, T4@11, T3@8. Undo order: 13, 12, 11, 8.
  assert.deepEqual(
    clrs.map((c) => (c as ClrRecord).prevLSN),
    [13, 12, 11, 8],
  );
});

test('undo: committing winner without END is closed out, not undone', () => {
  const system = new DatabaseSystem();
  const t1 = system.begin();
  system.update(t1, 1, 42);
  system.commit(t1);
  system.logManager.flushAll(); // make the END record durable first
  system.disk.truncateTail(1); // simulate crash between COMMIT flush and END
  system.crash();

  const { lm, bp, analysis } = recoverThroughRedo(system);
  const stats = undoPhase(analysis, lm, bp);
  assert.deepEqual(stats.losers, []);
  assert.deepEqual(stats.winners, [t1]);
  assert.equal(stats.clrCount, 0, 'winners are never undone');
  assert.deepEqual(stats.endedTxns, [t1], 'winner receives its END');
  assert.equal(bp.read(1), 42);
});

test('undo: crash mid-abort resumes via undoNextLSN without re-undoing CLRs', () => {
  const system = new DatabaseSystem();
  const t1 = system.begin();
  system.update(t1, 1, 10); // LSN 1
  system.update(t1, 2, 20); // LSN 2
  system.update(t1, 3, 30); // LSN 3
  system.abort(t1);         // ABORT + CLR(3) + CLR(2) + CLR(1) + END
  system.logManager.flushAll();
  // Crash mid-abort: keep ABORT + only the first CLR, drop the rest.
  system.disk.truncateTail(3); // drops CLR(2), CLR(1), END
  system.crash();

  const { lm, bp, analysis } = recoverThroughRedo(system);
  const stats = undoPhase(analysis, lm, bp);
  assert.equal(stats.clrCount, 2, 'only the two remaining UPDATEs are compensated');
  assert.equal(bp.read(1), 0);
  assert.equal(bp.read(2), 0);
  assert.equal(bp.read(3), 0);
  // Total CLRs in the log: 1 (pre-crash) + 2 (recovery) = 3, one per UPDATE.
  const clrs = lm.readAll().filter((r) => r.type === LogType.Clr);
  assert.equal(clrs.length, 3);
});
