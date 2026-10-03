import type { LSN, TxnStatus } from './log-record.ts';
import type { LogManager } from './log-manager.ts';
import type { StorageEngine } from './storage.ts';
import { LockAbortedError, type LockManager } from './lock-manager.ts';

export class TransactionAbortedError extends Error {
  readonly txnId: number;
  constructor(txnId: number) {
    super(`transaction ${txnId} was aborted`);
    this.name = 'TransactionAbortedError';
    this.txnId = txnId;
  }
}

interface TxnState {
  lastLSN: LSN;
  status: TxnStatus;
  wounded: boolean;
}

/**
 * Transaction manager. Writes follow the WAL protocol: the UPDATE record
 * is appended (and eventually forced) before the page may reach disk, and
 * the COMMIT record is forced synchronously at commit time. Runtime abort
 * rolls back along the prevLSN chain, logging a CLR per undone update so
 * that a crash mid-abort is itself recoverable.
 */
export class TransactionManager {
  private readonly log: LogManager;
  private readonly storage: StorageEngine;
  private readonly locks: LockManager;
  private readonly txns = new Map<number, TxnState>();
  private nextTxnId = 1;
  readonly committed = new Set<number>();
  readonly aborted = new Set<number>();

  constructor(log: LogManager, storage: StorageEngine, locks: LockManager) {
    this.log = log;
    this.storage = storage;
    this.locks = locks;
    this.locks.setVictimHandler((txnId) => {
      const state = this.txns.get(txnId);
      if (state && state.status === 'RUNNING') {
        state.wounded = true;
        this.abort(txnId);
      }
    });
  }

  begin(): number {
    const txnId = this.nextTxnId++;
    this.locks.registerTxn(txnId);
    const rec = this.log.append({ type: 'BEGIN', txnId, prevLSN: 0 });
    this.txns.set(txnId, { lastLSN: rec.lsn, status: 'RUNNING', wounded: false });
    return txnId;
  }

  async write(
    txnId: number,
    pageId: number,
    offset: number,
    value: string,
  ): Promise<void> {
    const state = this.mustBeAlive(txnId);
    await this.locks.acquire(txnId, `${pageId}:${offset}`, 'X');
    // The txn may have been wounded while it waited for the lock.
    this.mustBeAlive(txnId, state);
    const before = this.storage.readSlot(pageId, offset);
    const rec = this.log.append({
      type: 'UPDATE',
      txnId,
      prevLSN: state.lastLSN,
      pageId,
      offset,
      beforeImage: before,
      afterImage: value,
    });
    state.lastLSN = rec.lsn;
    this.storage.writeSlot(pageId, offset, value, rec.lsn);
  }

  async read(txnId: number, pageId: number, offset: number): Promise<string> {
    const state = this.mustBeAlive(txnId);
    await this.locks.acquire(txnId, `${pageId}:${offset}`, 'S');
    this.mustBeAlive(txnId, state);
    return this.storage.readSlot(pageId, offset);
  }

  /** Force the commit record, then release locks (strict 2PL). */
  commit(txnId: number): void {
    if (!this.txns.has(txnId)) {
      // The arbiter wounded and aborted this txn underneath us.
      throw new TransactionAbortedError(txnId);
    }
    const state = this.stateOf(txnId);
    if (state.wounded || state.status !== 'RUNNING') {
      this.abort(txnId);
      throw new TransactionAbortedError(txnId);
    }
    state.status = 'COMMITTING';
    const commitRec = this.log.append({
      type: 'COMMIT',
      txnId,
      prevLSN: state.lastLSN,
    });
    state.lastLSN = commitRec.lsn;
    this.log.flush(commitRec.lsn); // force-at-commit
    this.log.append({ type: 'END', txnId, prevLSN: state.lastLSN });
    this.finish(txnId);
    this.committed.add(txnId);
  }

  /** Roll back a running/aborting transaction, logging CLRs. Idempotent. */
  abort(txnId: number): void {
    const state = this.txns.get(txnId);
    if (!state || state.status === 'COMMITTING') return;
    if (state.status === 'RUNNING') {
      state.status = 'ABORTING';
      const abortRec = this.log.append({
        type: 'ABORT',
        txnId,
        prevLSN: state.lastLSN,
      });
      state.lastLSN = abortRec.lsn;
    }
    let lsn = state.lastLSN;
    while (lsn !== 0) {
      const rec = this.log.getRecord(lsn);
      if (rec.type === 'UPDATE') {
        const clr = this.log.append({
          type: 'CLR',
          txnId,
          prevLSN: state.lastLSN,
          pageId: rec.pageId,
          offset: rec.offset,
          beforeImage: rec.afterImage,
          afterImage: rec.beforeImage,
          undoNextLSN: rec.prevLSN,
        });
        state.lastLSN = clr.lsn;
        this.storage.writeSlot(
          rec.pageId!,
          rec.offset!,
          rec.beforeImage!,
          clr.lsn,
        );
        lsn = rec.prevLSN;
      } else if (rec.type === 'CLR') {
        lsn = rec.undoNextLSN ?? 0;
      } else {
        lsn = rec.prevLSN;
      }
    }
    this.log.append({ type: 'END', txnId, prevLSN: state.lastLSN });
    this.finish(txnId);
    this.aborted.add(txnId);
  }

  /** Non-fuzzy checkpoint: snapshot the txn table and DirtyPageTable. */
  checkpoint(): LSN {
    const activeTxns: Record<number, { lastLSN: LSN; status: TxnStatus }> = {};
    for (const [txnId, state] of this.txns) {
      activeTxns[txnId] = { lastLSN: state.lastLSN, status: state.status };
    }
    const rec = this.log.append({
      type: 'CHECKPOINT',
      txnId: 0,
      prevLSN: 0,
      checkpoint: {
        activeTxns,
        dirtyPages: Object.fromEntries(this.storage.dirtyPageTable()),
      },
    });
    return rec.lsn;
  }

  activeTxnCount(): number {
    return this.txns.size;
  }

  isActive(txnId: number): boolean {
    return this.txns.has(txnId);
  }

  private finish(txnId: number): void {
    this.locks.releaseAll(txnId);
    this.locks.unregisterTxn(txnId);
    this.txns.delete(txnId);
  }

  private stateOf(txnId: number): TxnState {
    const state = this.txns.get(txnId);
    if (!state) {
      throw new Error(`TransactionManager: unknown txn ${txnId}`);
    }
    return state;
  }

  private mustBeAlive(txnId: number, known?: TxnState): TxnState {
    const state = known ?? this.txns.get(txnId);
    if (!state) {
      // Vanished without our commit/abort: force-aborted by the arbiter.
      throw new TransactionAbortedError(txnId);
    }
    if (state.wounded || state.status !== 'RUNNING') {
      throw new TransactionAbortedError(txnId);
    }
    return state;
  }
}

export { LockAbortedError };
