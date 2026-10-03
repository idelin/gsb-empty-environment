/**
 * Transaction manager: normal (forward) processing.
 *
 * Generates UPDATE / COMMIT / ABORT / CLR / END / CHECKPOINT records and
 * applies changes through the buffer pool under the WAL protocol.
 */

import { NULL_LSN, LogRecordType } from './log-record.ts';
import type {
  LSN,
  PageID,
  TxnID,
  TxnTableEntrySnapshot,
  DirtyPageTableSnapshot,
} from './log-record.ts';
import type { WalManager } from './wal.ts';
import { LatchManager } from './storage.ts';
import type { BufferPool } from './storage.ts';
import { LockManager } from './locks.ts';

export const TxnStatus = {
  RUNNING: 'RUNNING',
  COMMITTED: 'COMMITTED',
  ABORTED: 'ABORTED',
} as const;
export type TxnStatus = (typeof TxnStatus)[keyof typeof TxnStatus];

export interface TxnState {
  txnId: TxnID;
  status: TxnStatus;
  lastLSN: LSN;
}

export class TransactionManager {
  private nextTxnId = 0;
  private txns = new Map<TxnID, TxnState>();
  private latchOwner = new Map<TxnID, symbol>();
  private readonly wal: WalManager;
  private readonly pool: BufferPool;
  private readonly latches: LatchManager;
  private readonly locks: LockManager;

  constructor(
    wal: WalManager,
    pool: BufferPool,
    latches: LatchManager = new LatchManager(),
    locks: LockManager = new LockManager(),
  ) {
    this.wal = wal;
    this.pool = pool;
    this.latches = latches;
    this.locks = locks;
  }

  begin(): TxnID {
    const txnId = ++this.nextTxnId;
    this.txns.set(txnId, { txnId, status: TxnStatus.RUNNING, lastLSN: NULL_LSN });
    this.latchOwner.set(txnId, Symbol(`txn-${txnId}`));
    return txnId;
  }

  private running(txnId: TxnID): TxnState {
    const s = this.txns.get(txnId);
    if (!s) throw new Error(`unknown transaction ${txnId}`);
    if (s.status !== TxnStatus.RUNNING) {
      throw new Error(`transaction ${txnId} is not running (status=${s.status})`);
    }
    return s;
  }

  /** Log + apply a page update. Returns the LSN of the UPDATE record. */
  update(txnId: TxnID, pageId: PageID, key: string, value: unknown): LSN {
    const s = this.running(txnId);
    // Strict 2PL: logical lock held until end of transaction.
    this.locks.acquire(txnId, pageId, key);
    const owner = this.latchOwner.get(txnId)!;
    this.latches.acquire(pageId, owner);
    try {
      const page = this.pool.fetch(pageId);
      const before = page.data[key];
      const rec = this.wal.append({
        type: LogRecordType.UPDATE,
        prevLSN: s.lastLSN,
        txnId,
        pageId,
        key,
        before,
        after: value,
      });
      page.data[key] = value;
      this.pool.markDirty(pageId, rec.lsn);
      s.lastLSN = rec.lsn;
      return rec.lsn;
    } finally {
      this.latches.release(pageId, owner);
    }
  }

  /** Commit: log COMMIT, flush log to the commit LSN, then log END. */
  commit(txnId: TxnID): void {
    const s = this.running(txnId);
    const commit = this.wal.append({
      type: LogRecordType.COMMIT,
      prevLSN: s.lastLSN,
      txnId,
    });
    s.status = TxnStatus.COMMITTED;
    s.lastLSN = commit.lsn;
    this.wal.flush(commit.lsn); // commit durability point
    const end = this.wal.append({
      type: LogRecordType.END,
      prevLSN: s.lastLSN,
      txnId,
    });
    s.lastLSN = end.lsn;
    this.txns.delete(txnId);
    this.latchOwner.delete(txnId);
    this.locks.releaseAll(txnId);
  }

  /**
   * Abort (normal processing): roll back in reverse order, writing a CLR
   * per undone UPDATE, then END. Same undo logic the recovery pass uses.
   */
  abort(txnId: TxnID): void {
    const s = this.running(txnId);
    const abort = this.wal.append({
      type: LogRecordType.ABORT,
      prevLSN: s.lastLSN,
      txnId,
    });
    s.status = TxnStatus.ABORTED;
    s.lastLSN = abort.lsn;
    this.undoChain(txnId, s.lastLSN);
    const end = this.wal.append({
      type: LogRecordType.END,
      prevLSN: s.lastLSN,
      txnId,
    });
    s.lastLSN = end.lsn;
    this.txns.delete(txnId);
    this.latchOwner.delete(txnId);
    this.locks.releaseAll(txnId);
  }

  /**
   * Reverse-scan undo of one transaction's prevLSN chain, writing a CLR
   * for every not-yet-compensated UPDATE. Shared by abort() and recovery.
   */
  undoChain(txnId: TxnID, fromLSN: LSN): void {
    const s = this.txns.get(txnId);
    if (!s) throw new Error(`unknown transaction ${txnId}`);
    let lsn = fromLSN;
    while (lsn !== NULL_LSN) {
      const rec = this.wal.get(lsn);
      if (rec.type === LogRecordType.CHECKPOINT) {
        throw new Error('checkpoint record in transaction chain');
      }
      if (rec.type === LogRecordType.UPDATE) {
        const owner = this.latchOwner.get(txnId)!;
        this.latches.acquire(rec.pageId, owner);
        try {
          const page = this.pool.fetch(rec.pageId);
          const clr = this.wal.append({
            type: LogRecordType.CLR,
            prevLSN: s.lastLSN,
            txnId,
            pageId: rec.pageId,
            key: rec.key,
            after: rec.before,
            undoneLSN: rec.lsn,
            undoNextLSN: rec.prevLSN,
          });
          if (rec.before === undefined) delete page.data[rec.key];
          else page.data[rec.key] = rec.before;
          this.pool.markDirty(rec.pageId, clr.lsn);
          s.lastLSN = clr.lsn;
          lsn = clr.undoNextLSN;
        } finally {
          this.latches.release(rec.pageId, owner);
        }
      } else if (rec.type === LogRecordType.CLR) {
        lsn = rec.undoNextLSN; // already compensated; skip
      } else {
        lsn = rec.prevLSN; // COMMIT / ABORT markers
      }
    }
  }

  /** Fuzzy checkpoint: snapshot the transaction table + dirty page table. */
  checkpoint(): LSN {
    const txnTable: TxnTableEntrySnapshot[] = [...this.txns.values()].map(
      (t) => ({ txnId: t.txnId, status: t.status, lastLSN: t.lastLSN }),
    );
    const dirtyPageTable: DirtyPageTableSnapshot = {};
    for (const [pageId, recLSN] of this.pool.dirtyPageTable()) {
      dirtyPageTable[pageId] = recLSN;
    }
    const rec = this.wal.append({
      type: LogRecordType.CHECKPOINT,
      txnTable,
      dirtyPageTable,
    });
    this.wal.flush(rec.lsn);
    return rec.lsn;
  }

  activeTxns(): ReadonlyMap<TxnID, TxnState> {
    return this.txns;
  }

  latchLeakCount(): number {
    return this.latches.heldCount;
  }

  lockLeakCount(): number {
    return this.locks.heldCount;
  }
}
