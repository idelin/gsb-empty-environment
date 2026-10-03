import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSystem } from '../src/system.ts';
import { analysisPhase } from '../src/recovery.ts';
import { LogType, TxnStatus, NULL_LSN } from '../src/types.ts';
import { buildAcceptanceScenario } from './helpers.ts';

test('analysis: empty log yields empty tables and null redoLSN', () => {
  const result = analysisPhase([]);
  assert.equal(result.txnTable.size, 0);
  assert.equal(result.dirtyPageTable.size, 0);
  assert.equal(result.redoLSN, NULL_LSN);
  assert.equal(result.checkpointLSN, NULL_LSN);
});

test('analysis: full scan without checkpoint rebuilds TT and DPT', () => {
  const system = new DatabaseSystem();
  const t1 = system.begin();
  const t2 = system.begin();
  system.update(t1, 1, 10); // LSN 1
  system.update(t2, 2, 20); // LSN 2
  system.update(t1, 1, 30); // LSN 3 (same page, recLSN stays 1)
  system.commit(t1);        // LSN 4,5 (COMMIT+END)
  system.logManager.flushAll();

  const result = analysisPhase(system.disk.readLog());
  assert.equal(result.checkpointLSN, NULL_LSN);
  // t1 ended -> removed; t2 still running.
  assert.deepEqual(
    [...result.txnTable.values()].map((e) => [e.txnId, e.status, e.lastLSN]),
    [[t2, TxnStatus.Running, 2]],
  );
  assert.deepEqual(
    [...result.dirtyPageTable.entries()],
    [
      [1, 1],
      [2, 2],
    ],
  );
  assert.equal(result.redoLSN, 1);
});

test('analysis: committing txn (COMMIT without END) is a winner candidate', () => {
  const system = new DatabaseSystem();
  const t1 = system.begin();
  system.update(t1, 1, 10);
  // Append COMMIT but crash before END is written.
  system.logManager.append({ type: LogType.Commit, txnId: t1, prevLSN: 1 });
  system.logManager.flushAll();

  const result = analysisPhase(system.disk.readLog());
  const entry = result.txnTable.get(t1);
  assert.equal(entry?.status, TxnStatus.Committing);
  assert.equal(entry?.lastLSN, 2);
});

test('analysis: aborting txn keeps ABORTING status across its CLRs', () => {
  const system = new DatabaseSystem();
  const t1 = system.begin();
  system.update(t1, 1, 10);
  system.update(t1, 2, 20);
  system.abort(t1); // writes ABORT + 2 CLRs + END... but crash before END:
  // rebuild scenario manually: remove the END record to simulate crash mid-abort.
  system.logManager.flushAll();
  system.disk.truncateTail(1); // drop END

  const result = analysisPhase(system.disk.readLog());
  const entry = result.txnTable.get(t1);
  assert.equal(entry?.status, TxnStatus.Aborting);
  // lastLSN points at the last CLR.
  const log = system.disk.readLog();
  const lastClr = [...log].reverse().find((r) => r.type === LogType.Clr);
  assert.equal(entry?.lastLSN, lastClr?.lsn);
});

test('analysis: starts from the last end-checkpoint and merges its snapshot', () => {
  const { system } = buildAcceptanceScenario();
  const result = analysisPhase(system.disk.readLog());

  assert.equal(result.checkpointLSN, 10);
  // T1/T2 ended before the checkpoint and must NOT appear.
  assert.deepEqual(
    [...result.txnTable.keys()].sort(),
    [3, 4, 5],
  );
  assert.equal(result.txnTable.get(3)?.lastLSN, 8);
  assert.equal(result.txnTable.get(4)?.lastLSN, 11);
  assert.equal(result.txnTable.get(5)?.lastLSN, 13);
  assert.deepEqual(
    [...result.dirtyPageTable.entries()],
    [
      [1, 1],
      [2, 3],
      [3, 2],
      [4, 12],
      [5, 13],
    ],
  );
  assert.equal(result.redoLSN, 1);
});

test('analysis: txn ended after the checkpoint is removed from the TT', () => {
  const system = new DatabaseSystem();
  const t1 = system.begin();
  system.update(t1, 1, 5);
  system.checkpoint(); // snapshot contains t1 running
  const t2 = system.begin();
  system.update(t2, 2, 9);
  system.commit(t1); // END after the checkpoint
  system.logManager.flushAll();

  const result = analysisPhase(system.disk.readLog());
  assert.equal(result.txnTable.has(t1), false, 'ended txn must be removed');
  assert.equal(result.txnTable.get(t2)?.status, TxnStatus.Running);
});
