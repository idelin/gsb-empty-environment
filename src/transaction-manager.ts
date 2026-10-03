import { TransactionStateError } from './errors.ts';
import { LockManager } from './lock-manager.ts';
import { ShrinkingPhaseManager } from './shrinking-phase-manager.ts';
import { DataStore } from './store.ts';
import { Transaction } from './transaction.ts';

interface CommitLogEntry {
  txnId: number;
  commitTimestamp: number;
}

/**
 * Owns the lock manager, the data store and the shrinking-phase manager,
 * hands out transactions and totally orders them by commit timestamp.
 * Because every transaction holds all of its locks until commit (SS2PL),
 * the resulting schedule is conflict-equivalent to the serial schedule in
 * commitTimestamp order.
 */
export class TransactionManager {
  readonly lockManager = new LockManager();
  readonly store = new DataStore();
  readonly shrinking: ShrinkingPhaseManager;

  private nextTxnId = 1;
  private commitSeq = 0;
  private readonly txns = new Map<number, Transaction>();
  private readonly commitLog: CommitLogEntry[] = [];

  constructor() {
    this.shrinking = new ShrinkingPhaseManager(this.lockManager, (txnId) => this.txns.get(txnId)?.phase);
  }

  begin(): Transaction {
    const txn = new Transaction(this.nextTxnId++, {
      lockManager: this.lockManager,
      store: this.store,
      shrinking: this.shrinking,
      assignCommitTimestamp: () => ++this.commitSeq,
      recordCommit: (committed) => {
        this.commitLog.push({ txnId: committed.id, commitTimestamp: committed.commitTimestamp! });
      },
    });
    this.txns.set(txn.id, txn);
    return txn;
  }

  get(txnId: number): Transaction | undefined {
    return this.txns.get(txnId);
  }

  /**
   * Simulates a sudden crash of the executor running the given transaction.
   * Recovery is immediate and safe: the transaction's modifications are
   * rolled back and every lock it held (or waited for) is revoked.
   */
  injectCrash(txnId: number): void {
    const txn = this.txns.get(txnId);
    if (!txn) throw new TransactionStateError(`unknown transaction ${txnId}`);
    txn.abort('crash');
  }

  /** Transaction ids ordered by commit timestamp = the equivalent serial order. */
  commitOrder(): number[] {
    return [...this.commitLog]
      .sort((a, b) => a.commitTimestamp - b.commitTimestamp)
      .map((entry) => entry.txnId);
  }
}
