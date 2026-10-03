import test from 'node:test';
import assert from 'node:assert/strict';
import { LogType } from '../src/types.ts';
import type { ClrRecord } from '../src/types.ts';
import {
  buildAcceptanceScenario,
  ACCEPTANCE_EXPECTED_PAGES,
  assertNoLeaks,
  assertLogIntegrity,
  assertAllTxnsEnded,
} from './helpers.ts';

test('acceptance: 3 uncommitted + 2 committed txns, checkpoint, crash, 3-phase recovery', () => {
  const { system, faults, t1, t2, t3, t4, t5 } = buildAcceptanceScenario();

  // Pre-crash: WAL is durable through LSN 13, checkpoint at LSN 9-10.
  const wal = system.disk.readLog();
  assert.equal(wal.length, 13);
  assert.equal(wal[8]?.type, LogType.BeginCheckpoint);
  assert.equal(wal[9]?.type, LogType.EndCheckpoint);
  assert.equal(system.disk.pageValue(1), 111, 'stolen page carries uncommitted data');

  faults.powerLoss();
  assert.ok(system.isCrashed);

  const report = system.recover();

  // --- Analysis ---
  assert.equal(report.analysis.checkpointLSN, 10);
  assert.deepEqual([...report.analysis.txnTable.keys()].sort(), [t3, t4, t5]);
  assert.equal(report.analysis.redoLSN, 1);
  assert.deepEqual(
    [...report.analysis.dirtyPageTable.entries()],
    [
      [1, 1],
      [2, 3],
      [3, 2],
      [4, 12],
      [5, 13],
    ],
  );

  // --- Redo: history repeated ---
  assert.equal(report.redo.applied, 5);
  assert.equal(report.redo.skipped, 2);

  // --- Undo: losers rolled back with CLRs ---
  assert.deepEqual(report.undo.losers, [t3, t4, t5]);
  assert.deepEqual(report.undo.winners, []);
  assert.equal(report.undo.clrCount, 4);

  // --- Final state: committed persisted, uncommitted rolled back ---
  for (const [pageId, want] of ACCEPTANCE_EXPECTED_PAGES) {
    assert.equal(system.disk.pageValue(pageId), want, `page ${pageId}`);
  }

  // --- Strict no-leak assertions ---
  assertNoLeaks(system, report);
  assertLogIntegrity(system);
  assertAllTxnsEnded(system, [t1, t2, t3, t4, t5]);

  // --- CLR chain in the durable log ---
  const log = system.disk.readLog();
  const clrs = log.filter((r): r is ClrRecord => r.type === LogType.Clr);
  assert.deepEqual(
    clrs.map((c) => [c.txnId, c.prevLSN, c.undoNextLSN]),
    [
      [t5, 13, 12],
      [t5, 12, 0],
      [t4, 11, 0],
      [t3, 8, 0],
    ],
  );
});

test('acceptance: recovery is idempotent — a second crash changes nothing', () => {
  const { system, faults, t1, t2, t3, t4, t5 } = buildAcceptanceScenario();
  faults.powerLoss();
  const first = system.recover();
  const logLengthAfterFirst = system.disk.readLog().length;

  faults.powerLoss();
  const second = system.recover();

  assert.equal(second.analysis.txnTable.size, 0, 'no losers remain');
  assert.equal(second.undo.clrCount, 0, 'no new CLRs are written');
  assert.equal(system.disk.readLog().length, logLengthAfterFirst, 'log does not grow');
  for (const [pageId, want] of ACCEPTANCE_EXPECTED_PAGES) {
    assert.equal(system.disk.pageValue(pageId), want, `page ${pageId}`);
  }
  assertNoLeaks(system, second);
  assertLogIntegrity(system);
  assertAllTxnsEnded(system, [t1, t2, t3, t4, t5]);
  assert.ok(first.undo.clrCount > 0);
});

test('acceptance: system is usable after recovery (self-healing continues)', () => {
  const { system, faults } = buildAcceptanceScenario();
  faults.powerLoss();
  system.recover();

  const t6 = system.begin();
  assert.ok(t6 > 5, 'txn ids continue past the pre-crash range');
  system.update(t6, 1, 999);
  system.commit(t6);
  system.flushAllPages();
  assert.equal(system.disk.pageValue(1), 999);
  assert.equal(system.activeTxnCount, 0);
  assertLogIntegrity(system);
});
