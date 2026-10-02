import { LockManager } from './lockManager.ts';
import { ShrinkPhaseManager } from './monitor.ts';
import { ScheduleHistory } from './schedule.ts';
import { DataStore } from './store.ts';
import { TransactionStateError } from './types.ts';
import type { TxState } from './types.ts';

export interface Transaction {
  readonly id: number;
  state: TxState;
  commitTs?: number;
}

interface UndoEntry {
  key: string;
  before: number | undefined;
}

/**
 * TransactionManager — SS2PL kernel.
 *
 * - Growing phase: read/write acquire locks; nothing is ever released.
 * - Shrinking point: commit/abort/crash-recovery releases ALL locks in
 *   one atomic step, guarded by the ShrinkPhaseManager.
 * - Writes go through to the store immediately with an undo log, so a
 *   crash triggers a real rollback of modifications.
 * - Each commit is stamped with a monotonically increasing timestamp;
 *   ScheduleHistory verifies the schedule equals commit-ts ordering.
 */
export class TransactionManager {
  private nextTxId = 1;
  private nextCommitTs = 1;
  private readonly transactions = new Map<number, Transaction>();
  private readonly undoLogs = new Map<number, UndoEntry[]>();
  readonly monitor = new ShrinkPhaseManager();
  readonly locks = new LockManager(this.monitor);
  readonly history = new ScheduleHistory();

  readonly store: DataStore;

  constructor(store: DataStore = new DataStore()) {
    this.store = store;
  }

  begin(): Transaction {
    const tx: Transaction = { id: this.nextTxId++, state: 'ACTIVE' };
    this.transactions.set(tx.id, tx);
    this.undoLogs.set(tx.id, []);
    this.monitor.register(tx.id);
    return tx;
  }

  private requireActive(tx: Transaction): void {
    if (tx.state !== 'ACTIVE') {
      throw new TransactionStateError(`tx${tx.id} is ${tx.state}, expected ACTIVE`);
    }
  }

  async read(tx: Transaction, key: string): Promise<number | undefined> {
    this.requireActive(tx);
    await this.locks.acquire(tx.id, key, 'S');
    this.history.record({ kind: 'read', txId: tx.id, key });
    return this.store.get(key);
  }

  async write(tx: Transaction, key: string, value: number): Promise<void> {
    this.requireActive(tx);
    await this.locks.acquire(tx.id, key, 'X');
    this.undoLogs.get(tx.id)!.push({ key, before: this.store.get(key) });
    this.store.set(key, value);
    this.history.record({ kind: 'write', txId: tx.id, key });
  }

  /** Shrinking point: stamp commit ts, then release everything at once. */
  commit(tx: Transaction): number {
    this.requireActive(tx);
    tx.state = 'COMMITTING';
    this.monitor.setPhase(tx.id, 'COMMITTING');
    tx.commitTs = this.nextCommitTs++;
    this.history.record({ kind: 'commit', txId: tx.id, commitTs: tx.commitTs });
    this.atomicRelease(tx.id);
    tx.state = 'COMMITTED';
    this.monitor.setPhase(tx.id, 'COMMITTED');
    this.undoLogs.delete(tx.id);
    return tx.commitTs;
  }

  /** Shrinking point for a normal abort: undo writes, release everything. */
  abort(tx: Transaction): void {
    if (tx.state !== 'ACTIVE') {
      throw new TransactionStateError(`tx${tx.id} is ${tx.state}, expected ACTIVE`);
    }
    this.rollback(tx);
    tx.state = 'ABORTED';
    this.monitor.setPhase(tx.id, 'ABORTED');
  }

  /**
   * Inject a crash: the transaction stops dead WITHOUT any cleanup.
   * Its locks stay held and its writes stay applied until `recover()`.
   */
  crash(tx: Transaction): void {
    this.requireActive(tx);
    tx.state = 'CRASHED';
    this.monitor.setPhase(tx.id, 'CRASHED');
  }

  /**
   * Recovery pass: find crashed transactions, roll back their
   * modifications (undo log, reverse order) and release all their locks.
   */
  recover(): number[] {
    const recovered: number[] = [];
    for (const tx of this.transactions.values()) {
      if (tx.state !== 'CRASHED') continue;
      this.rollback(tx);
      tx.state = 'ABORTED';
      this.monitor.setPhase(tx.id, 'ABORTED');
      recovered.push(tx.id);
    }
    return recovered;
  }

  private rollback(tx: Transaction): void {
    tx.state = 'ABORTING';
    this.monitor.setPhase(tx.id, 'ABORTING');
    const undo = this.undoLogs.get(tx.id) ?? [];
    for (let i = undo.length - 1; i >= 0; i--) {
      const { key, before } = undo[i];
      if (before === undefined) this.store.delete(key);
      else this.store.set(key, before);
    }
    this.history.record({ kind: 'abort', txId: tx.id });
    this.atomicRelease(tx.id);
    this.undoLogs.delete(tx.id);
  }

  /** The single, atomic shrinking point enforced by the monitor. */
  private atomicRelease(txId: number): void {
    this.monitor.beginAtomicRelease(txId);
    try {
      this.locks.releaseAll(txId);
    } finally {
      this.monitor.endAtomicRelease(txId);
    }
  }

  stateOf(txId: number): TxState | undefined {
    return this.transactions.get(txId)?.state;
  }
}
