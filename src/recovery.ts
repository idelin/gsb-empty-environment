import { LogType, TxnStatus, NULL_LSN } from './types.ts';
import type {
  LogRecord,
  LSN,
  PageId,
  TxnId,
  TxnTableEntry,
} from './types.ts';
import type { LogManager } from './logManager.ts';
import type { BufferPool } from './bufferPool.ts';
import type { SimulatedDisk } from './disk.ts';

export interface AnalysisResult {
  txnTable: Map<TxnId, TxnTableEntry>;
  dirtyPageTable: Map<PageId, LSN>;
  redoLSN: LSN;
  /** LSN of the end-checkpoint record analysis started from (0 = full scan). */
  checkpointLSN: LSN;
}

export interface RedoStats {
  applied: number;
  skipped: number;
}

export interface UndoStats {
  losers: TxnId[];
  winners: TxnId[];
  clrCount: number;
  endedTxns: TxnId[];
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redo: RedoStats;
  undo: UndoStats;
  /** Transaction table after recovery completes; empty means no leaks. */
  finalTxnTable: Map<TxnId, TxnTableEntry>;
}

/* ------------------------------------------------------------------ */
/* Phase 1: Analysis                                                   */
/* ------------------------------------------------------------------ */

/**
 * Rebuilds the transaction table and dirty page table by scanning the log
 * forward from the most recent (fuzzy) end-checkpoint, or from the start of
 * the log when no checkpoint exists.
 */
export function analysisPhase(log: LogRecord[]): AnalysisResult {
  const txnTable = new Map<TxnId, TxnTableEntry>();
  const dirtyPageTable = new Map<PageId, LSN>();

  let startIndex = 0;
  let checkpointLSN: LSN = NULL_LSN;
  for (let i = log.length - 1; i >= 0; i--) {
    const rec = log[i];
    if (rec && rec.type === LogType.EndCheckpoint) {
      checkpointLSN = rec.lsn;
      for (const entry of rec.txnTable) txnTable.set(entry.txnId, { ...entry });
      for (const entry of rec.dirtyPageTable) dirtyPageTable.set(entry.pageId, entry.recLSN);
      startIndex = i + 1;
      break;
    }
  }

  for (let i = startIndex; i < log.length; i++) {
    const rec = log[i];
    if (!rec) continue;
    switch (rec.type) {
      case LogType.Update:
      case LogType.Clr: {
        const entry = txnTable.get(rec.txnId);
        txnTable.set(rec.txnId, {
          txnId: rec.txnId,
          status: entry?.status === TxnStatus.Aborting ? TxnStatus.Aborting : TxnStatus.Running,
          lastLSN: rec.lsn,
        });
        if (!dirtyPageTable.has(rec.pageId)) dirtyPageTable.set(rec.pageId, rec.lsn);
        break;
      }
      case LogType.Commit:
        txnTable.set(rec.txnId, { txnId: rec.txnId, status: TxnStatus.Committing, lastLSN: rec.lsn });
        break;
      case LogType.Abort:
        txnTable.set(rec.txnId, { txnId: rec.txnId, status: TxnStatus.Aborting, lastLSN: rec.lsn });
        break;
      case LogType.End:
        txnTable.delete(rec.txnId);
        break;
      case LogType.BeginCheckpoint:
      case LogType.EndCheckpoint:
        break;
    }
  }

  let redoLSN: LSN = NULL_LSN;
  for (const recLSN of dirtyPageTable.values()) {
    if (redoLSN === NULL_LSN || recLSN < redoLSN) redoLSN = recLSN;
  }
  return { txnTable, dirtyPageTable, redoLSN, checkpointLSN };
}

/* ------------------------------------------------------------------ */
/* Phase 2: Redo (repeating history)                                   */
/* ------------------------------------------------------------------ */

/**
 * Repeats history from the smallest recLSN in the DPT. Every UPDATE and CLR
 * whose page is dirty and whose effects are not yet on disk is re-applied,
 * restoring the exact pre-crash state — including changes of loser txns.
 */
export function redoPhase(
  log: LogRecord[],
  analysis: AnalysisResult,
  disk: SimulatedDisk,
  bufferPool: BufferPool,
): RedoStats {
  const stats: RedoStats = { applied: 0, skipped: 0 };
  for (const rec of log) {
    if (rec.lsn < analysis.redoLSN) continue;
    if (rec.type !== LogType.Update && rec.type !== LogType.Clr) continue;
    const recLSN = analysis.dirtyPageTable.get(rec.pageId);
    if (recLSN === undefined || recLSN > rec.lsn) {
      stats.skipped++;
      continue;
    }
    if (bufferPool.pageLSN(rec.pageId) >= rec.lsn) {
      stats.skipped++;
      continue;
    }
    bufferPool.update(rec.pageId, rec.afterImage, rec.lsn);
    stats.applied++;
  }
  return stats;
}

/* ------------------------------------------------------------------ */
/* Phase 3: Undo (loser rollback with CLRs)                            */
/* ------------------------------------------------------------------ */

/** Simple max-heap over LSNs: undo processes records in strict reverse order. */
export class MaxHeap {
  private items: number[] = [];

  get size(): number {
    return this.items.length;
  }

  push(value: number): void {
    this.items.push(value);
    let i = this.items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if ((this.items[parent] ?? 0) >= (this.items[i] ?? 0)) break;
      [this.items[parent], this.items[i]] = [this.items[i] ?? 0, this.items[parent] ?? 0];
      i = parent;
    }
  }

  pop(): number | undefined {
    const top = this.items[0];
    const last = this.items.pop();
    if (this.items.length > 0 && last !== undefined) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let largest = i;
        if (left < this.items.length && (this.items[left] ?? 0) > (this.items[largest] ?? 0)) largest = left;
        if (right < this.items.length && (this.items[right] ?? 0) > (this.items[largest] ?? 0)) largest = right;
        if (largest === i) break;
        [this.items[largest], this.items[i]] = [this.items[i] ?? 0, this.items[largest] ?? 0];
        i = largest;
      }
    }
    return top;
  }
}

/**
 * Undoes every loser transaction in reverse LSN order, writing a CLR for each
 * compensated UPDATE. CLRs carry undoNextLSN so that a crash during undo (or
 * a re-run of recovery) never undoes the same update twice.
 */
export function undoPhase(
  analysis: AnalysisResult,
  logManager: LogManager,
  bufferPool: BufferPool,
): UndoStats {
  const records = new Map<LSN, LogRecord>();
  for (const rec of logManager.readAll()) records.set(rec.lsn, rec);

  const losers: TxnId[] = [];
  const winners: TxnId[] = [];
  const ended = new Set<TxnId>();
  const heap = new MaxHeap();

  for (const entry of analysis.txnTable.values()) {
    if (entry.status === TxnStatus.Committing) {
      winners.push(entry.txnId);
    } else {
      losers.push(entry.txnId);
      heap.push(entry.lastLSN);
    }
  }

  let clrCount = 0;

  // Global reverse-order scan across all loser txns (largest LSN first).
  while (heap.size > 0) {
    const lsn = heap.pop();
    if (lsn === undefined) break;
    const rec = records.get(lsn);
    if (!rec) continue;
    const txnId = (rec as { txnId?: TxnId }).txnId;
    if (txnId === undefined) continue;

    if (rec.type === LogType.Update) {
      const clrLSN = logManager.append({
        type: LogType.Clr,
        txnId,
        prevLSN: lsn,
        undoNextLSN: rec.prevLSN,
        pageId: rec.pageId,
        beforeImage: rec.afterImage,
        afterImage: rec.beforeImage,
      });
      bufferPool.update(rec.pageId, rec.beforeImage, clrLSN);
      clrCount++;
      if (rec.prevLSN !== NULL_LSN) {
        heap.push(rec.prevLSN);
      } else {
        logManager.append({ type: LogType.End, txnId, prevLSN: clrLSN });
        ended.add(txnId);
      }
    } else if (rec.type === LogType.Clr) {
      if (rec.undoNextLSN !== NULL_LSN) {
        heap.push(rec.undoNextLSN);
      } else if (!ended.has(txnId)) {
        logManager.append({ type: LogType.End, txnId, prevLSN: lsn });
        ended.add(txnId);
      }
    } else if (rec.type === LogType.Abort) {
      if (rec.prevLSN !== NULL_LSN) {
        heap.push(rec.prevLSN);
      } else if (!ended.has(txnId)) {
        logManager.append({ type: LogType.End, txnId, prevLSN: lsn });
        ended.add(txnId);
      }
    }
  }

  // Winners that committed but never got their END record are closed out.
  for (const txnId of winners) {
    const entry = analysis.txnTable.get(txnId);
    if (!entry) continue;
    logManager.append({ type: LogType.End, txnId, prevLSN: entry.lastLSN });
    ended.add(txnId);
  }

  return { losers, winners, clrCount, endedTxns: [...ended].sort((a, b) => a - b) };
}

/* ------------------------------------------------------------------ */
/* Full ARIES recovery                                                 */
/* ------------------------------------------------------------------ */

export function recover(
  disk: SimulatedDisk,
  logManager: LogManager,
  bufferPool: BufferPool,
): RecoveryReport {
  const log = logManager.readAll();
  const analysis = analysisPhase(log);
  const redo = redoPhase(log, analysis, disk, bufferPool);
  const undo = undoPhase(analysis, logManager, bufferPool);

  // Persist everything: log first (WAL), then all dirty pages.
  logManager.flushAll();
  bufferPool.flushAll();

  const finalTxnTable = new Map<TxnId, TxnTableEntry>();
  for (const entry of analysis.txnTable.values()) {
    if (!undo.endedTxns.includes(entry.txnId)) finalTxnTable.set(entry.txnId, entry);
  }
  return { analysis, redo, undo, finalTxnTable };
}
