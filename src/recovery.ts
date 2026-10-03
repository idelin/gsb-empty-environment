/**
 * ARIES crash-recovery engine.
 *
 * Protocol state machine (strictly ordered, deterministic):
 *
 *   IDLE --recover()--> ANALYSIS --(scan ckpt..eof)--> REDO
 *     --(repeat history from min recLSN)--> UNDO
 *     --(reverse scan, CLRs, END records)--> DONE
 *
 * Invariants enforced here:
 *  - Analysis rebuilds TransactionTable + DirtyPageTable from the most
 *    recent checkpoint and the log tail.
 *  - Redo repeats history: every logged update/CLR whose effect is not
 *    yet on disk (pageLSN < rec.lsn) is reapplied, losers included.
 *  - Undo rolls back loser transactions in globally descending LSN order,
 *    writing one CLR per undone UPDATE (CLRs are never undone), and an
 *    END record when a transaction's chain is exhausted.
 */

import { NULL_LSN, LogRecordType, isPageModifying } from './log-record.ts';
import type { LSN, PageID, TxnID } from './log-record.ts';
import type { WalManager } from './wal.ts';
import { BufferPool, LatchManager } from './storage.ts';
import type { Disk } from './storage.ts';

export const RecoveryPhase = {
  IDLE: 'IDLE',
  ANALYSIS: 'ANALYSIS',
  REDO: 'REDO',
  UNDO: 'UNDO',
  DONE: 'DONE',
} as const;
export type RecoveryPhase = (typeof RecoveryPhase)[keyof typeof RecoveryPhase];

const LEGAL_TRANSITIONS: Readonly<Record<RecoveryPhase, readonly RecoveryPhase[]>> = {
  IDLE: [RecoveryPhase.ANALYSIS],
  ANALYSIS: [RecoveryPhase.REDO],
  REDO: [RecoveryPhase.UNDO],
  UNDO: [RecoveryPhase.DONE],
  DONE: [],
};

export interface TxnTableEntry {
  txnId: TxnID;
  status: 'RUNNING' | 'COMMITTED' | 'ABORTED';
  lastLSN: LSN;
}

export class RecoveryProtocolError extends Error {}

export class RecoveryEngine {
  private phase: RecoveryPhase = RecoveryPhase.IDLE;
  private txnTable = new Map<TxnID, TxnTableEntry>();
  private dirtyPageTable = new Map<PageID, LSN>();
  private checkpointLSN: LSN = NULL_LSN;
  private redoLSN: LSN = NULL_LSN;
  private readonly pool: BufferPool;
  private readonly latches = new LatchManager();
  private readonly undoOwner = Symbol('recovery-undo');
  private readonly wal: WalManager;

  constructor(wal: WalManager, disk: Disk) {
    this.wal = wal;
    this.pool = new BufferPool(disk, wal);
  }

  getPhase(): RecoveryPhase {
    return this.phase;
  }

  private transition(to: RecoveryPhase): void {
    if (!LEGAL_TRANSITIONS[this.phase].includes(to)) {
      throw new RecoveryProtocolError(
        `illegal recovery transition ${this.phase} -> ${to}`,
      );
    }
    this.phase = to;
  }

  getTxnTable(): ReadonlyMap<TxnID, TxnTableEntry> {
    return this.txnTable;
  }

  getDirtyPageTable(): ReadonlyMap<PageID, LSN> {
    return this.dirtyPageTable;
  }

  getCheckpointLSN(): LSN {
    return this.checkpointLSN;
  }

  getRedoLSN(): LSN {
    return this.redoLSN;
  }

  /** LSNs of CLR records written by the undo pass (for verification). */
  private writtenCLRs: LSN[] = [];
  getWrittenCLRs(): readonly LSN[] {
    return this.writtenCLRs;
  }

  /** ---------------- ANALYSIS ---------------- */

  runAnalysis(): void {
    this.transition(RecoveryPhase.ANALYSIS);
    const log = this.wal.all();

    // Locate the most recent checkpoint.
    let startIdx = 0;
    for (let i = log.length - 1; i >= 0; i--) {
      const rec = log[i]!;
      if (rec.type === LogRecordType.CHECKPOINT) {
        this.checkpointLSN = rec.lsn;
        for (const t of rec.txnTable) {
          this.txnTable.set(t.txnId, { txnId: t.txnId, status: t.status, lastLSN: t.lastLSN });
        }
        for (const [pageId, recLSN] of Object.entries(rec.dirtyPageTable)) {
          this.dirtyPageTable.set(pageId, recLSN);
        }
        startIdx = i + 1;
        break;
      }
    }

    // Scan forward from the checkpoint, maintaining TT + DPT.
    for (let i = startIdx; i < log.length; i++) {
      const rec = log[i]!;
      if (rec.type === LogRecordType.CHECKPOINT) continue;
      const entry = this.txnTable.get(rec.txnId) ?? {
        txnId: rec.txnId,
        status: 'RUNNING' as const,
        lastLSN: NULL_LSN,
      };
      entry.lastLSN = rec.lsn;
      switch (rec.type) {
        case LogRecordType.UPDATE:
        case LogRecordType.CLR:
          this.txnTable.set(rec.txnId, entry);
          if (!this.dirtyPageTable.has(rec.pageId)) {
            this.dirtyPageTable.set(rec.pageId, rec.lsn);
          }
          break;
        case LogRecordType.COMMIT:
          entry.status = 'COMMITTED';
          this.txnTable.set(rec.txnId, entry);
          break;
        case LogRecordType.ABORT:
          entry.status = 'ABORTED';
          this.txnTable.set(rec.txnId, entry);
          break;
        case LogRecordType.END:
          this.txnTable.delete(rec.txnId);
          break;
      }
    }
  }

  /** ---------------- REDO (repeat history) ---------------- */

  runRedo(): void {
    this.transition(RecoveryPhase.REDO);

    // Redo starts at the smallest recLSN in the DPT.
    let redoLSN: LSN = NULL_LSN;
    for (const recLSN of this.dirtyPageTable.values()) {
      if (redoLSN === NULL_LSN || recLSN < redoLSN) redoLSN = recLSN;
    }
    this.redoLSN = redoLSN;
    if (redoLSN === NULL_LSN) return; // nothing dirty: nothing to redo

    const log = this.wal.all();
    for (const rec of log) {
      if (rec.lsn < redoLSN || !isPageModifying(rec)) continue;
      const recLSN = this.dirtyPageTable.get(rec.pageId);
      if (recLSN === undefined) continue; // page not dirty: skip
      if (rec.lsn < recLSN) continue; // older than recLSN: already on disk
      const page = this.pool.fetch(rec.pageId);
      if (page.pageLSN >= rec.lsn) continue; // already applied: skip
      if (rec.after === undefined) delete page.data[rec.key];
      else page.data[rec.key] = rec.after;
      page.pageLSN = rec.lsn;
      this.pool.markDirty(rec.pageId, rec.lsn);
    }
  }

  /** ---------------- UNDO (losers, globally descending LSN) ---------------- */

  runUndo(): void {
    this.transition(RecoveryPhase.UNDO);

    // Losers: transactions still active (RUNNING or ABORTED) at end of analysis.
    const losers = [...this.txnTable.values()].filter(
      (t) => t.status === 'RUNNING' || t.status === 'ABORTED',
    );

    // Max-heap (by LSN) of records still to process.
    const heap: LSN[] = [];
    const push = (l: LSN) => {
      heap.push(l);
      let i = heap.length - 1;
      while (i > 0) {
        const p = (i - 1) >> 1;
        if (heap[p]! >= heap[i]!) break;
        [heap[p], heap[i]] = [heap[i]!, heap[p]!];
        i = p;
      }
    };
    const popMax = (): LSN | undefined => {
      if (heap.length === 0) return undefined;
      const top = heap[0]!;
      const last = heap.pop()!;
      if (heap.length > 0) {
        heap[0] = last;
        let i = 0;
        for (;;) {
          const l = 2 * i + 1;
          const r = l + 1;
          let m = i;
          if (l < heap.length && heap[l]! > heap[m]!) m = l;
          if (r < heap.length && heap[r]! > heap[m]!) m = r;
          if (m === i) break;
          [heap[m], heap[i]] = [heap[i]!, heap[m]!];
          i = m;
        }
      }
      return top;
    };
    for (const t of losers) if (t.lastLSN !== NULL_LSN) push(t.lastLSN);

    let lsn: LSN | undefined;
    while ((lsn = popMax()) !== undefined) {
      const rec = this.wal.get(lsn);
      if (rec.type === LogRecordType.CHECKPOINT) {
        throw new RecoveryProtocolError('checkpoint record in undo chain');
      }
      const txnId = rec.txnId;
      if (rec.type === LogRecordType.UPDATE) {
        // Write a CLR and apply the compensation to the page.
        const entry = this.txnTable.get(txnId)!;
        this.latches.acquire(rec.pageId, this.undoOwner);
        try {
          const page = this.pool.fetch(rec.pageId);
          const clr = this.wal.append({
            type: LogRecordType.CLR,
            prevLSN: entry.lastLSN,
            txnId,
            pageId: rec.pageId,
            key: rec.key,
            after: rec.before,
            undoneLSN: rec.lsn,
            undoNextLSN: rec.prevLSN,
          });
          this.writtenCLRs.push(clr.lsn);
          if (rec.before === undefined) delete page.data[rec.key];
          else page.data[rec.key] = rec.before;
          this.pool.markDirty(rec.pageId, clr.lsn);
          entry.lastLSN = clr.lsn;
          if (clr.undoNextLSN !== NULL_LSN) push(clr.undoNextLSN);
          else this.endTransaction(txnId);
        } finally {
          this.latches.release(rec.pageId, this.undoOwner);
        }
      } else if (rec.type === LogRecordType.CLR) {
        if (rec.undoNextLSN !== NULL_LSN) push(rec.undoNextLSN);
        else this.endTransaction(txnId);
      } else {
        // ABORT (or stray COMMIT): keep walking the chain.
        if (rec.prevLSN !== NULL_LSN) push(rec.prevLSN);
        else this.endTransaction(txnId);
      }
    }
  }

  private endTransaction(txnId: TxnID): void {
    const entry = this.txnTable.get(txnId);
    if (!entry) return; // already ended (defensive; chain is deterministic)
    const end = this.wal.append({
      type: LogRecordType.END,
      prevLSN: entry.lastLSN,
      txnId,
    });
    entry.lastLSN = end.lsn;
    this.txnTable.delete(txnId);
  }

  /** Durability barrier: flush log, then all dirty pages; closes the protocol. */
  complete(): void {
    this.transition(RecoveryPhase.DONE);
    // Log first (WAL), then every dirty page.
    this.wal.flush(this.wal.getLastLSN());
    this.pool.flushAll();
  }

  /** Run the full protocol, then make the recovered state durable. */
  recover(): void {
    this.runAnalysis();
    this.runRedo();
    this.runUndo();
    this.complete();
  }

  dirtyPagesAfterRecovery(): PageID[] {
    return this.pool.dirtyPages();
  }

  latchLeakCount(): number {
    return this.latches.heldCount;
  }
}
