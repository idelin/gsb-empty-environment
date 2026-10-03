import { DeadlockError, TransactionAbortedError, TransactionStateError } from './errors.ts';
import type { LockManager, LockMode } from './lock-manager.ts';
import type { ShrinkingPhaseManager, TransactionState } from './shrinking-phase-manager.ts';
import type { DataStore } from './store.ts';

export type AbortReason = 'user' | 'deadlock' | 'crash';

export interface TransactionContext {
  lockManager: LockManager;
  store: DataStore;
  shrinking: ShrinkingPhaseManager;
  assignCommitTimestamp: () => number;
  recordCommit: (txn: Transaction) => void;
}

interface UndoEntry {
  key: string;
  existed: boolean;
  previous: number | undefined;
}

/**
 * A strict-two-phase-locked transaction.
 *
 * Growing phase: read()/write() only ever acquire locks; there is no API to
 * release an individual lock, so partial early release cannot happen.
 * Shrinking phase: commit()/abort() flip the state first and then let the
 * ShrinkingPhaseManager drop every lock atomically.
 */
export class Transaction {
  readonly id: number;
  readonly startedAt: number;
  commitTimestamp: number | null = null;
  abortReason: AbortReason | null = null;

  private state: TransactionState = 'growing';
  private readonly ctx: TransactionContext;
  private readonly undoLog: UndoEntry[] = [];
  private readonly writtenValues = new Map<string, number>();

  constructor(id: number, ctx: TransactionContext) {
    this.id = id;
    this.ctx = ctx;
    this.startedAt = Date.now();
  }

  get phase(): TransactionState {
    return this.state;
  }

  /** Final value this transaction wrote per key (used for schedule verification). */
  writes(): ReadonlyMap<string, number> {
    return this.writtenValues;
  }

  private ensureGrowing(): void {
    if (this.state !== 'growing') {
      throw new TransactionStateError(`transaction ${this.id} is ${this.state}; no further operations allowed`);
    }
  }

  private async acquire(key: string, mode: LockMode): Promise<void> {
    try {
      await this.ctx.lockManager.acquire(this.id, key, mode);
    } catch (err) {
      if (err instanceof DeadlockError) {
        // Chosen as deadlock victim: roll back immediately so the survivor
        // can proceed, then surface the error to the caller.
        this.abort('deadlock');
      }
      throw err;
    }
  }

  async read(key: string): Promise<number | undefined> {
    this.ensureGrowing();
    await this.acquire(key, 'shared');
    return this.ctx.store.read(key);
  }

  async write(key: string, value: number): Promise<void> {
    this.ensureGrowing();
    await this.acquire(key, 'exclusive');
    if (!this.writtenValues.has(key)) {
      this.undoLog.push({ key, existed: this.ctx.store.has(key), previous: this.ctx.store.read(key) });
    }
    this.ctx.store.write(key, value);
    this.writtenValues.set(key, value);
  }

  commit(): void {
    this.ensureGrowing();
    this.state = 'committed';
    this.commitTimestamp = this.ctx.assignCommitTimestamp();
    this.ctx.recordCommit(this);
    this.ctx.shrinking.enterShrinkingPhase(this.id);
  }

  abort(reason: AbortReason = 'user'): void {
    if (this.state !== 'growing') return;
    this.state = 'aborted';
    this.abortReason = reason;
    // Undo writes in reverse chronological order before any lock is
    // released, so no other transaction can observe the dirty values.
    for (let i = this.undoLog.length - 1; i >= 0; i--) {
      const entry = this.undoLog[i];
      if (entry.existed) this.ctx.store.write(entry.key, entry.previous!);
      else this.ctx.store.delete(entry.key);
    }
    this.undoLog.length = 0;
    this.writtenValues.clear();
    this.ctx.lockManager.cancelWaiting(this.id, new TransactionAbortedError(this.id, reason));
    this.ctx.shrinking.enterShrinkingPhase(this.id);
  }
}
