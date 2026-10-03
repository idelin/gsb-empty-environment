import assert from 'node:assert/strict';
import { DatabaseSystem } from '../src/system.ts';
import { FaultInjector } from '../src/fault.ts';
import { LogType } from '../src/types.ts';
import type { TxnId } from '../src/types.ts';
import type { RecoveryReport } from '../src/recovery.ts';

export interface AcceptanceScenario {
  system: DatabaseSystem;
  faults: FaultInjector;
  t1: TxnId; // committed
  t2: TxnId; // committed
  t3: TxnId; // uncommitted (active across the checkpoint)
  t4: TxnId; // uncommitted
  t5: TxnId; // uncommitted
}

/**
 * Builds the acceptance WAL: 2 committed + 3 uncommitted transactions, a
 * fuzzy checkpoint in between, one stolen dirty page, then a power loss.
 *
 * LSN layout:
 *   1 U T1 P1->100   2 U T2 P3->300   3 U T1 P2->200
 *   4 C T1  5 E T1   6 C T2  7 E T2
 *   8 U T3 P1->111   9 BEGIN_CKPT    10 END_CKPT
 *  11 U T4 P2->222  12 U T5 P4->444  13 U T5 P5->555
 */
export function buildAcceptanceScenario(): AcceptanceScenario {
  const system = new DatabaseSystem();
  const faults = new FaultInjector(system);

  const t1 = system.begin();
  const t2 = system.begin();
  system.update(t1, 1, 100);
  system.update(t2, 3, 300);
  system.update(t1, 2, 200);
  system.commit(t1);
  system.commit(t2);

  const t3 = system.begin();
  system.update(t3, 1, 111);

  system.checkpoint();

  const t4 = system.begin();
  const t5 = system.begin();
  system.update(t4, 2, 222);
  system.update(t5, 4, 444);
  system.update(t5, 5, 555);

  system.logManager.flushAll();
  system.bufferPool.flushPage(1); // STEAL: uncommitted P1=111 hits disk
  return { system, faults, t1, t2, t3, t4, t5 };
}

export const ACCEPTANCE_EXPECTED_PAGES = new Map([
  [1, 100],
  [2, 200],
  [3, 300],
  [4, 0],
  [5, 0],
]);

/** Strict leak checks: nothing in flight may survive recovery. */
export function assertNoLeaks(system: DatabaseSystem, report: RecoveryReport): void {
  assert.equal(report.finalTxnTable.size, 0, 'transaction table must be empty after recovery');
  assert.equal(system.bufferPool.pinnedCount, 0, 'no page may stay pinned');
  assert.equal(system.lockManager.lockCount, 0, 'no lock may be held');
  assert.equal(system.logManager.bufferedCount, 0, 'log buffer must be fully flushed');
  assert.equal(system.activeTxnCount, 0, 'no active transactions may remain');
  assert.equal(system.bufferPool.dirtyCount, 0, 'no dirty page may remain in the buffer pool');
}

/** Log integrity: contiguous LSNs from 1 and valid checksums on every record. */
export function assertLogIntegrity(system: DatabaseSystem): void {
  const raw = system.disk.rawLog();
  const validated = system.disk.readLog();
  assert.equal(validated.length, raw.length, 'durable log must have no gaps or corrupt records');
  validated.forEach((rec, i) => assert.equal(rec.lsn, i + 1, 'LSNs must be contiguous from 1'));
}

/** Every transaction that ever began must be closed by an END record. */
export function assertAllTxnsEnded(system: DatabaseSystem, txnIds: TxnId[]): void {
  const log = system.disk.readLog();
  for (const txnId of txnIds) {
    const ended = log.some((r) => r.type === LogType.End && 'txnId' in r && r.txnId === txnId);
    assert.ok(ended, `txn ${txnId} must have an END record`);
  }
}
