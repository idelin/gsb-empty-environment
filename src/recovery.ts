/**
 * ARIES crash recovery: Analysis -> Redo (repeat history) -> Undo (with CLRs).
 *
 * - Analysis rebuilds the Transaction Table (ATT) and Dirty Page Table (DPT)
 *   starting from the most recent checkpoint.
 * - Redo repeats history from the smallest recLSN, re-applying UPDATE and
 *   CLR after-images unless the page already reflects them (pageLSN check).
 * - Undo rolls back loser transactions in reverse LSN order, writing a
 *   compensation log record (CLR) per undone update; CLRs carry
 *   undoNextLSN so restarted recovery never undoes the same action twice.
 */

import { Wal } from "./wal";
import {
  LogRecord,
  RecordType,
  TxnEntry,
  UpdateRecord,
  ClrRecord,
} from "./logrecord";
import { BufferPool, PageStore } from "./storage";

export interface AnalysisResult {
  /** Active transaction table after analysis (winners + losers). */
  txns: Map<number, TxnEntry>;
  /** Dirty page table: pageId -> recLSN. */
  dirtyPages: Map<number, number>;
  lastCheckpointLSN: number;
  redoLSN: number;
  endLSN: number;
}

export interface RedoResult {
  appliedLSNs: number[];
  skipped: number;
}

export interface UndoResult {
  clrLSNs: number[];
  endedTxns: number[];
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redo: RedoResult;
  undo: UndoResult;
  /** Every resource the engine opened has been released. */
  resourcesClosed: boolean;
}

// ------------------------------------------------------------- analysis --

export function analysis(wal: Wal): AnalysisResult {
  // First pass: locate the most recent checkpoint.
  let lastCheckpointLSN = 0;
  for (const frame of wal.scan()) {
    if (frame.record.type === RecordType.Checkpoint) {
      lastCheckpointLSN = frame.lsn;
    }
  }

  const txns = new Map<number, TxnEntry>();
  const dirtyPages = new Map<number, number>();
  const endLSN = wal.nextLSN;

  if (lastCheckpointLSN !== 0) {
    const ckpt = wal.readRecord(lastCheckpointLSN);
    if (ckpt.type !== RecordType.Checkpoint) {
      throw new Error("checkpoint LSN does not point at a checkpoint record");
    }
    for (const t of ckpt.txns) txns.set(t.txnId, { ...t });
    for (const [pageId, recLSN] of ckpt.dirtyPages) dirtyPages.set(pageId, recLSN);
  }

  for (const frame of wal.scan(lastCheckpointLSN)) {
    const rec = frame.record;
    switch (rec.type) {
      case RecordType.Begin:
        txns.set(rec.txnId, { txnId: rec.txnId, status: "running", lastLSN: rec.lsn });
        break;
      case RecordType.Update:
      case RecordType.Clr: {
        const entry = txns.get(rec.txnId);
        if (entry) entry.lastLSN = rec.lsn;
        else txns.set(rec.txnId, { txnId: rec.txnId, status: "running", lastLSN: rec.lsn });
        if (!dirtyPages.has(rec.pageId)) dirtyPages.set(rec.pageId, rec.lsn);
        break;
      }
      case RecordType.Commit: {
        const entry = txns.get(rec.txnId);
        if (entry) {
          entry.status = "committed";
          entry.lastLSN = rec.lsn;
        } else {
          txns.set(rec.txnId, { txnId: rec.txnId, status: "committed", lastLSN: rec.lsn });
        }
        break;
      }
      case RecordType.Abort: {
        const entry = txns.get(rec.txnId);
        if (entry) {
          entry.status = "aborting";
          entry.lastLSN = rec.lsn;
        } else {
          txns.set(rec.txnId, { txnId: rec.txnId, status: "aborting", lastLSN: rec.lsn });
        }
        break;
      }
      case RecordType.End:
        txns.delete(rec.txnId);
        break;
      case RecordType.Checkpoint:
        break;
    }
  }

  let redoLSN = endLSN;
  for (const recLSN of dirtyPages.values()) {
    if (recLSN < redoLSN) redoLSN = recLSN;
  }
  return { txns, dirtyPages, lastCheckpointLSN, redoLSN, endLSN };
}

// ----------------------------------------------------------------- redo --

export function redo(wal: Wal, result: AnalysisResult, pool: BufferPool): RedoResult {
  const appliedLSNs: number[] = [];
  let skipped = 0;
  if (result.dirtyPages.size === 0) return { appliedLSNs, skipped };

  for (const frame of wal.scan(result.redoLSN)) {
    const rec = frame.record;
    if (rec.type !== RecordType.Update && rec.type !== RecordType.Clr) continue;
    const recLSN = result.dirtyPages.get(rec.pageId);
    if (recLSN === undefined || frame.lsn < recLSN) {
      skipped++;
      continue; // page was clean when this record was written
    }
    if (pool.pageLSN(rec.pageId) >= frame.lsn) {
      skipped++;
      continue; // page already reflects this record
    }
    pool.applyBytes(rec.pageId, rec.offset, rec.after, frame.lsn);
    appliedLSNs.push(frame.lsn);
  }

  // Persist every page that was dirty at crash time.
  for (const pageId of result.dirtyPages.keys()) pool.flushPage(pageId);
  return { appliedLSNs, skipped };
}

// ----------------------------------------------------------------- undo --

/** Max-heap of LSNs so losers are undone in strictly reverse log order. */
class LsnHeap {
  private items: Array<{ lsn: number; txnId: number }> = [];

  get size(): number {
    return this.items.length;
  }

  push(lsn: number, txnId: number): void {
    this.items.push({ lsn, txnId });
    let i = this.items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.items[parent]!.lsn >= this.items[i]!.lsn) break;
      [this.items[parent], this.items[i]] = [this.items[i]!, this.items[parent]!];
      i = parent;
    }
  }

  pop(): { lsn: number; txnId: number } | undefined {
    const top = this.items[0];
    const last = this.items.pop();
    if (this.items.length > 0 && last) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let largest = i;
        if (l < this.items.length && this.items[l]!.lsn > this.items[largest]!.lsn) largest = l;
        if (r < this.items.length && this.items[r]!.lsn > this.items[largest]!.lsn) largest = r;
        if (largest === i) break;
        [this.items[largest], this.items[i]] = [this.items[i]!, this.items[largest]!];
        i = largest;
      }
    }
    return top;
  }
}

export function undo(wal: Wal, result: AnalysisResult, pool: BufferPool): UndoResult {
  const clrLSNs: number[] = [];
  const endedTxns: number[] = [];

  // Winners (committed but not yet ended) get their END records now.
  for (const [txnId, entry] of [...result.txns]) {
    if (entry.status === "committed") {
      wal.appendSync({ type: RecordType.End, lsn: 0, txnId, prevLSN: entry.lastLSN });
      result.txns.delete(txnId);
      endedTxns.push(txnId);
    }
  }

  // Losers that were still running are switched to aborting (ABORT record).
  const heap = new LsnHeap();
  for (const entry of result.txns.values()) {
    if (entry.status === "running") {
      const abortLSN = wal.appendSync({
        type: RecordType.Abort, lsn: 0, txnId: entry.txnId, prevLSN: entry.lastLSN,
      });
      entry.status = "aborting";
      entry.lastLSN = abortLSN;
    }
    heap.push(entry.lastLSN, entry.txnId);
  }

  for (;;) {
    const item = heap.pop();
    if (!item) break;
    const rec = wal.readRecord(item.lsn);
    const entry = result.txns.get(rec.txnId);
    if (!entry) throw new Error(`undo: txn ${rec.txnId} missing from ATT`);

    let nextLSN: number;
    if (rec.type === RecordType.Update) {
      nextLSN = undoUpdate(wal, pool, rec, entry);
      clrLSNs.push(entry.lastLSN);
      if (!result.dirtyPages.has(rec.pageId)) {
        result.dirtyPages.set(rec.pageId, entry.lastLSN);
      }
    } else if (rec.type === RecordType.Clr) {
      nextLSN = rec.undoNextLSN; // already compensated: skip over it
    } else {
      nextLSN = rec.prevLSN; // begin/abort/commit/end: walk the chain
    }

    if (nextLSN === 0) {
      wal.appendSync({ type: RecordType.End, lsn: 0, txnId: rec.txnId, prevLSN: entry.lastLSN });
      result.txns.delete(rec.txnId);
      endedTxns.push(rec.txnId);
    } else {
      heap.push(nextLSN, rec.txnId);
    }
  }

  pool.flushAll();
  return { clrLSNs, endedTxns };
}

function undoUpdate(
  wal: Wal,
  pool: BufferPool,
  rec: UpdateRecord,
  entry: TxnEntry,
): number {
  const current = pool.readBytes(rec.pageId, rec.offset, rec.before.length);
  const clr: ClrRecord = {
    type: RecordType.Clr,
    lsn: 0,
    txnId: rec.txnId,
    prevLSN: entry.lastLSN,
    pageId: rec.pageId,
    offset: rec.offset,
    before: current,          // image replaced by the compensation
    after: rec.before,        // restore the update's before-image
    undoNextLSN: rec.prevLSN, // skip already-undone records on restart
  };
  const clrLSN = wal.appendSync(clr);
  pool.applyBytes(rec.pageId, rec.offset, rec.before, clrLSN);
  entry.lastLSN = clrLSN;
  return rec.prevLSN;
}

// -------------------------------------------------------------- recover --

/** Full restart recovery over a database directory (wal.log + data.db). */
export function recover(walPath: string, dataPath: string): RecoveryReport {
  const wal = Wal.open(walPath);
  const store = new PageStore(dataPath);
  const pool = new BufferPool(store);
  try {
    const analysisResult = analysis(wal);
    const redoResult = redo(wal, analysisResult, pool);
    const undoResult = undo(wal, analysisResult, pool);
    if (analysisResult.txns.size !== 0) {
      throw new Error(`recovery leak: ${analysisResult.txns.size} txns still active`);
    }
    if (pool.dirtyPageIds.length !== 0) {
      throw new Error("recovery leak: dirty pages left in buffer pool");
    }
    return { analysis: analysisResult, redo: redoResult, undo: undoResult, resourcesClosed: true };
  } finally {
    pool.close();
    store.close();
    wal.close();
  }
}
