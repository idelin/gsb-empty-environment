import type { MetricsRegistry } from '../metrics/registry.ts';
import { BufferPool, DiskStorage } from './storage.ts';
import {
  RecordType,
  TxnStatus,
  type ClrRecord,
  type DirtyPageEntry,
  type EndCheckpointRecord,
  type ErrorCode,
  type LogRecord,
  type TxnTableEntry,
  type UpdateRecord,
} from './types.ts';
import { readWal, WalWriter, type WalError } from './wal.ts';

export interface AnalysisResult {
  /** LSN of the begin-checkpoint the scan started from (null: full scan). */
  checkpointLSN: number | null;
  /** First LSN Redo must consider: min recLSN of the DPT (null: nothing). */
  redoLSN: number | null;
  txnTable: TxnTableEntry[];
  dirtyPages: DirtyPageEntry[];
  recordsScanned: number;
}

export interface RedoResult {
  applied: number;
  skippedNotDirty: number;
  skippedPageLSN: number;
}

export interface UndoResult {
  clrs: number;
  endedTxns: number[];
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redo: RedoResult;
  undo: UndoResult;
  errors: WalError[];
}

/** Max-heap over LSNs driving the Undo reverse scan. */
class LsnHeap {
  private items: { lsn: number; txnId: number }[] = [];

  push(lsn: number, txnId: number): void {
    this.items.push({ lsn, txnId });
    let i = this.items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.items[parent].lsn >= this.items[i].lsn) break;
      [this.items[parent], this.items[i]] = [this.items[i], this.items[parent]];
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
        const left = 2 * i + 1;
        const right = left + 1;
        let largest = i;
        if (left < this.items.length && this.items[left].lsn > this.items[largest].lsn) largest = left;
        if (right < this.items.length && this.items[right].lsn > this.items[largest].lsn) largest = right;
        if (largest === i) break;
        [this.items[largest], this.items[i]] = [this.items[i], this.items[largest]];
        i = largest;
      }
    }
    return top;
  }

  get size(): number {
    return this.items.length;
  }
}

/**
 * ARIES crash recovery: Analysis rebuilds the transaction and dirty-page
 * tables from the last checkpoint, Redo repeats history from the oldest
 * recLSN, and Undo rolls back loser transactions via CLRs in reverse LSN
 * order. Every phase is instrumented with fine-grained metrics.
 */
export class RecoveryEngine {
  private readonly walPath: string;
  private readonly storagePath: string;
  private readonly metrics?: MetricsRegistry;
  private readonly bufferCapacity: number;

  constructor(
    walPath: string,
    storagePath: string,
    options: { metrics?: MetricsRegistry; bufferCapacity?: number } = {},
  ) {
    this.walPath = walPath;
    this.storagePath = storagePath;
    this.metrics = options.metrics;
    this.bufferCapacity = options.bufferCapacity ?? 1024;
  }

  private recordError(code: ErrorCode): void {
    this.metrics?.counter('aries_errors_total', { code }).inc();
  }

  private phaseTimer(phase: string): () => number {
    const histogram = this.metrics?.histogram('aries_phase_duration_ns', [], {
      phase,
    });
    if (!histogram || !this.metrics) return () => 0;
    return this.metrics.startTimer(histogram);
  }

  recover(): RecoveryReport {
    const { records, errors } = readWal(this.walPath);
    for (const error of errors) {
      this.recordError(error.code);
    }
    const byLsn = new Map<number, LogRecord>();
    for (const record of records) {
      byLsn.set(record.lsn, record);
    }

    const analysis = this.analysis(records);

    const storage = new DiskStorage(this.storagePath);
    const buffer = new BufferPool(storage, this.bufferCapacity, this.metrics);

    const redo = this.redo(analysis, records, buffer);
    const undo = this.undo(analysis, byLsn, buffer);

    buffer.flushAll();
    return { analysis, redo, undo, errors };
  }

  /** Phase 1: rebuild TT/DPT from the most recent complete checkpoint. */
  private analysis(records: LogRecord[]): AnalysisResult {
    const stop = this.phaseTimer('analysis');
    try {
      let endCheckpoint: EndCheckpointRecord | null = null;
      for (const record of records) {
        if (record.type === RecordType.EndCheckpoint) endCheckpoint = record;
      }

      const txnTable = new Map<number, TxnTableEntry>();
      const dirtyPages = new Map<string, number>();
      let scanFrom = 0;
      let checkpointLSN: number | null = null;
      if (endCheckpoint) {
        checkpointLSN = endCheckpoint.beginLSN;
        scanFrom = endCheckpoint.beginLSN;
        for (const entry of endCheckpoint.txnTable) {
          txnTable.set(entry.txnId, { ...entry });
        }
        for (const entry of endCheckpoint.dirtyPages) {
          dirtyPages.set(entry.pageId, entry.recLSN);
        }
      }

      let scanned = 0;
      for (const record of records) {
        if (record.lsn < scanFrom) continue;
        scanned++;
        switch (record.type) {
          case RecordType.Update:
          case RecordType.Clr: {
            if (!txnTable.has(record.txnId)) {
              txnTable.set(record.txnId, {
                txnId: record.txnId,
                status: TxnStatus.Running,
                lastLSN: record.lsn,
              });
            }
            const txn = txnTable.get(record.txnId) as TxnTableEntry;
            txn.lastLSN = record.lsn;
            if (!dirtyPages.has(record.pageId)) {
              dirtyPages.set(record.pageId, record.lsn);
            }
            break;
          }
          case RecordType.Commit: {
            const txn = txnTable.get(record.txnId);
            if (txn) {
              txn.status = TxnStatus.Committed;
              txn.lastLSN = record.lsn;
            }
            break;
          }
          case RecordType.Abort: {
            const txn = txnTable.get(record.txnId);
            if (txn) {
              txn.status = TxnStatus.Aborting;
              txn.lastLSN = record.lsn;
            }
            break;
          }
          case RecordType.End:
            txnTable.delete(record.txnId);
            break;
          default:
            break;
        }
      }
      this.metrics?.counter('aries_log_records_scanned_total', { phase: 'analysis' }).inc(scanned);

      const txnList = [...txnTable.values()].map((e) => ({ ...e }));
      const dirtyList = [...dirtyPages.entries()].map(([pageId, recLSN]) => ({
        pageId,
        recLSN,
      }));
      this.metrics?.gauge('aries_analysis_txn_table_size').set(txnList.length);
      this.metrics?.gauge('aries_analysis_dirty_page_table_size').set(dirtyList.length);

      let redoLSN: number | null = null;
      for (const { recLSN } of dirtyList) {
        if (redoLSN === null || recLSN < redoLSN) redoLSN = recLSN;
      }
      return {
        checkpointLSN,
        redoLSN,
        txnTable: txnList,
        dirtyPages: dirtyList,
        recordsScanned: scanned,
      };
    } finally {
      stop();
    }
  }

  /** Phase 2: repeat history — reapply every logged update not yet on disk. */
  private redo(
    analysis: AnalysisResult,
    records: LogRecord[],
    buffer: BufferPool,
  ): RedoResult {
    const stop = this.phaseTimer('redo');
    const result: RedoResult = { applied: 0, skippedNotDirty: 0, skippedPageLSN: 0 };
    try {
      if (analysis.redoLSN === null) return result;
      const dpt = new Map(analysis.dirtyPages.map((e) => [e.pageId, e.recLSN]));
      let scanned = 0;
      for (const record of records) {
        if (record.lsn < analysis.redoLSN) continue;
        if (record.type !== RecordType.Update && record.type !== RecordType.Clr) continue;
        scanned++;
        const recLSN = dpt.get(record.pageId);
        if (recLSN === undefined || recLSN > record.lsn) {
          result.skippedNotDirty++;
          continue;
        }
        const page = buffer.fetch(record.pageId);
        if (page.pageLSN >= record.lsn) {
          result.skippedPageLSN++;
          continue;
        }
        page.value = (record as UpdateRecord | ClrRecord).after;
        page.pageLSN = record.lsn;
        buffer.markDirty(record.pageId, record.lsn);
        result.applied++;
      }
      this.metrics?.counter('aries_log_records_scanned_total', { phase: 'redo' }).inc(scanned);
      this.metrics?.counter('aries_redo_records_total', { result: 'applied' }).inc(result.applied);
      this.metrics?.counter('aries_redo_records_total', { result: 'skipped_not_dirty' }).inc(result.skippedNotDirty);
      this.metrics?.counter('aries_redo_records_total', { result: 'skipped_page_lsn' }).inc(result.skippedPageLSN);
      return result;
    } finally {
      stop();
    }
  }

  /** Phase 3: roll back losers in reverse LSN order, logging CLRs. */
  private undo(
    analysis: AnalysisResult,
    byLsn: Map<number, LogRecord>,
    buffer: BufferPool,
  ): UndoResult {
    const stop = this.phaseTimer('undo');
    const result: UndoResult = { clrs: 0, endedTxns: [] };
    const wal = new WalWriter(this.walPath);
    try {
      const txnTable = new Map(analysis.txnTable.map((e) => [e.txnId, { ...e }]));
      const heap = new LsnHeap();
      for (const entry of txnTable.values()) {
        if (entry.status === TxnStatus.Committed) {
          // Committed before crash but END never made it to the log.
          this.writeEnd(wal, entry);
          txnTable.delete(entry.txnId);
          result.endedTxns.push(entry.txnId);
        } else if (entry.lastLSN > 0) {
          heap.push(entry.lastLSN, entry.txnId);
        } else {
          txnTable.delete(entry.txnId);
          result.endedTxns.push(entry.txnId);
        }
      }

      let scanned = 0;
      while (heap.size > 0) {
        const { lsn, txnId } = heap.pop() as { lsn: number; txnId: number };
        const record = byLsn.get(lsn);
        if (!record) continue;
        scanned++;
        const txn = txnTable.get(txnId);
        if (!txn) continue;

        if (record.type === RecordType.Update) {
          const page = buffer.fetch(record.pageId);
          const clr = wal.append({
            type: RecordType.Clr,
            txnId,
            prevLSN: txn.lastLSN,
            pageId: record.pageId,
            undoNextLSN: record.prevLSN,
            after: record.before,
          });
          page.value = record.before;
          page.pageLSN = clr.lsn;
          buffer.markDirty(record.pageId, clr.lsn);
          txn.lastLSN = clr.lsn;
          result.clrs++;
          this.followChain(heap, wal, txnTable, txn, record.prevLSN, result);
        } else if (record.type === RecordType.Clr) {
          // Never undo an undo: jump straight to undoNextLSN.
          this.followChain(heap, wal, txnTable, txn, record.undoNextLSN, result);
        } else {
          this.followChain(heap, wal, txnTable, txn, record.prevLSN, result);
        }
      }
      this.metrics?.counter('aries_log_records_scanned_total', { phase: 'undo' }).inc(scanned);
      this.metrics?.counter('aries_undo_clr_total').inc(result.clrs);
      this.metrics?.gauge('aries_undo_ended_txns').set(result.endedTxns.length);
      return result;
    } finally {
      stop();
    }
  }

  /** Pushes the next record of a loser's chain, or closes the transaction. */
  private followChain(
    heap: LsnHeap,
    wal: WalWriter,
    txnTable: Map<number, TxnTableEntry>,
    txn: TxnTableEntry,
    nextLSN: number,
    result: UndoResult,
  ): void {
    if (nextLSN > 0) {
      heap.push(nextLSN, txn.txnId);
    } else {
      this.writeEnd(wal, txn);
      txnTable.delete(txn.txnId);
      result.endedTxns.push(txn.txnId);
    }
  }

  private writeEnd(wal: WalWriter, txn: TxnTableEntry): void {
    wal.append({
      type: RecordType.End,
      txnId: txn.txnId,
      prevLSN: txn.lastLSN,
    });
  }
}
