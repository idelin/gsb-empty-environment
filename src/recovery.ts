import {
  LogRecordType,
  NULL_LSN,
  TxnStatus,
  isRedoable,
  type CheckpointRecord,
  type LSN,
  type LogRecord,
  type PageId,
  type TxnId,
  type TxnTableEntry,
} from "./log-record.ts";
import { Mutex } from "./mutex.ts";
import type { BufferPool } from "./storage.ts";
import type { WriteAheadLog } from "./wal.ts";

export interface AnalysisResult {
  txnTable: Map<TxnId, TxnTableEntry>;
  dirtyPageTable: Map<PageId, LSN>;
  /** LSN where the analysis scan started (checkpoint or log start). */
  startLSN: LSN;
}

export interface RecoveryReport {
  analysis: {
    txnTable: TxnTableEntry[];
    dirtyPageTable: { pageId: PageId; recLSN: LSN }[];
  };
  redo: { scanned: number; applied: number; skipped: number; fromLSN: LSN };
  undo: {
    loserTxns: TxnId[];
    winnerTxns: TxnId[];
    clrWritten: number;
    endWritten: number;
  };
}

/**
 * ARIES restart-recovery engine.
 *
 *  - Analysis: rebuild the transaction table and dirty page table by scanning
 *    forward from the most recent checkpoint.
 *  - Redo: repeat history from the oldest recLSN, guarded by pageLSN so it is
 *    idempotent (re-running Redo never double-applies).
 *  - Undo: roll back loser transactions in reverse LSN order, writing a CLR
 *    per undone UPDATE. CLRs carry undoNextLSN so that undoing a transaction
 *    that was already mid-abort at crash time never undoes the same update
 *    twice (compensation is itself logged and idempotent).
 *
 * recover() is serialized by a mutex: concurrent invocations queue up and
 * every run after the first is a verified no-op.
 */
export class RecoveryEngine {
  private readonly recoveryMutex = new Mutex();
  private lastReport: RecoveryReport | undefined;

  private readonly wal: WriteAheadLog;
  private readonly pool: BufferPool;

  constructor(wal: WriteAheadLog, pool: BufferPool) {
    this.wal = wal;
    this.pool = pool;
  }

  get lock(): Mutex {
    return this.recoveryMutex;
  }

  get report(): RecoveryReport | undefined {
    return this.lastReport;
  }

  async recover(): Promise<RecoveryReport> {
    return this.recoveryMutex.runExclusive(async () => {
      const log = this.wal.allRecords();
      const analysis = this.analysis(log);
      const redoStats = this.redo(log, analysis);
      const undoStats = await this.undo(analysis);
      this.pool.flushAll();
      this.wal.flush();
      const report: RecoveryReport = {
        analysis: {
          txnTable: [...analysis.txnTable.values()].map((t) => ({ ...t })),
          dirtyPageTable: [...analysis.dirtyPageTable.entries()].map(
            ([pageId, recLSN]) => ({ pageId, recLSN }),
          ),
        },
        redo: redoStats,
        undo: undoStats,
      };
      this.lastReport = report;
      return report;
    });
  }

  /** Phase 1 — rebuild txn table + DPT from the last checkpoint forward. */
  analysis(log: LogRecord[]): AnalysisResult {
    const txnTable = new Map<TxnId, TxnTableEntry>();
    const dirtyPageTable = new Map<PageId, LSN>();

    let startIndex = 0;
    let checkpoint: CheckpointRecord | undefined;
    for (let i = log.length - 1; i >= 0; i -= 1) {
      const record = log[i];
      if (record && record.type === LogRecordType.CHECKPOINT) {
        checkpoint = record;
        startIndex = i;
        break;
      }
    }
    if (checkpoint) {
      for (const entry of checkpoint.txnTable) {
        txnTable.set(entry.txnId, { ...entry });
      }
      for (const entry of checkpoint.dirtyPageTable) {
        dirtyPageTable.set(entry.pageId, entry.recLSN);
      }
    }

    for (let i = startIndex; i < log.length; i += 1) {
      const record = log[i];
      if (!record) continue;
      switch (record.type) {
        case LogRecordType.BEGIN:
          txnTable.set(record.txnId, {
            txnId: record.txnId,
            status: TxnStatus.RUNNING,
            lastLSN: record.lsn,
          });
          break;
        case LogRecordType.UPDATE:
        case LogRecordType.CLR: {
          const txn = txnTable.get(record.txnId);
          if (txn) {
            txn.lastLSN = record.lsn;
          } else {
            txnTable.set(record.txnId, {
              txnId: record.txnId,
              status: TxnStatus.RUNNING,
              lastLSN: record.lsn,
            });
          }
          if (!dirtyPageTable.has(record.pageId)) {
            dirtyPageTable.set(record.pageId, record.lsn);
          }
          break;
        }
        case LogRecordType.COMMIT: {
          const txn = txnTable.get(record.txnId);
          if (txn) {
            txn.status = TxnStatus.COMMITTED;
            txn.lastLSN = record.lsn;
          }
          break;
        }
        case LogRecordType.ABORT: {
          const txn = txnTable.get(record.txnId);
          if (txn) {
            txn.status = TxnStatus.ABORTING;
            txn.lastLSN = record.lsn;
          }
          break;
        }
        case LogRecordType.END:
          txnTable.delete(record.txnId);
          break;
        case LogRecordType.CHECKPOINT:
          break;
      }
    }

    return {
      txnTable,
      dirtyPageTable,
      startLSN: checkpoint ? checkpoint.lsn : NULL_LSN,
    };
  }

  /** Phase 2 — repeat history from the oldest recLSN. Idempotent via pageLSN. */
  redo(
    log: LogRecord[],
    analysis: AnalysisResult,
  ): { scanned: number; applied: number; skipped: number; fromLSN: LSN } {
    let fromLSN: LSN = NULL_LSN;
    for (const recLSN of analysis.dirtyPageTable.values()) {
      if (fromLSN === NULL_LSN || recLSN < fromLSN) fromLSN = recLSN;
    }

    let scanned = 0;
    let applied = 0;
    let skipped = 0;
    for (const record of log) {
      if (record.lsn < fromLSN) continue;
      if (!isRedoable(record)) continue;
      scanned += 1;
      const recLSN = analysis.dirtyPageTable.get(record.pageId);
      if (recLSN === undefined || record.lsn < recLSN) {
        skipped += 1; // page was not dirty when this record was written
        continue;
      }
      const page = this.pool.fetchPage(record.pageId);
      if (page.pageLSN >= record.lsn) {
        skipped += 1; // already reflected on the page — idempotent skip
        continue;
      }
      this.pool.applyLogged(record.pageId, record.after, record.lsn);
      applied += 1;
    }
    return { scanned, applied, skipped, fromLSN };
  }

  /** Write the END record finalizing a fully-undone loser transaction. */
  private async finishTxn(
    txnId: TxnId,
    analysis: AnalysisResult,
    byLSN: Map<LSN, LogRecord>,
  ): Promise<number> {
    const prev = analysis.txnTable.get(txnId)?.lastLSN ?? NULL_LSN;
    const end = await this.wal.append({
      type: LogRecordType.END,
      txnId,
      prevLSN: prev,
    });
    byLSN.set(end.lsn, end);
    return 1;
  }

  /**
   * Phase 3 — reverse-scan compensation for loser transactions.
   *
   * A max-heap over the losers' lastLSN drives a global reverse-LSN walk.
   * For each UPDATE we emit a CLR whose undoNextLSN points at the update's
   * prevLSN; for each CLR encountered we jump straight to its undoNextLSN,
   * skipping updates already compensated before the crash. A transaction
   * whose chain reaches BEGIN (or NULL_LSN) is finalized with END.
   */
  async undo(analysis: AnalysisResult): Promise<{
    loserTxns: TxnId[];
    winnerTxns: TxnId[];
    clrWritten: number;
    endWritten: number;
  }> {
    const loserTxns: TxnId[] = [];
    const winnerTxns: TxnId[] = [];
    const toUndo = new Map<TxnId, LSN>();
    for (const txn of analysis.txnTable.values()) {
      if (txn.status === TxnStatus.COMMITTED) {
        winnerTxns.push(txn.txnId);
      } else {
        loserTxns.push(txn.txnId);
        toUndo.set(txn.txnId, txn.lastLSN);
      }
    }

    let clrWritten = 0;
    let endWritten = 0;
    const byLSN = new Map<LSN, LogRecord>();
    for (const record of this.wal.allRecords()) byLSN.set(record.lsn, record);

    while (toUndo.size > 0) {
      // Pick the globally largest LSN still to process (reverse scan order).
      let pickTxn: TxnId | undefined;
      let pickLSN: LSN = NULL_LSN;
      for (const [txnId, lsn] of toUndo) {
        if (pickTxn === undefined || lsn > pickLSN) {
          pickTxn = txnId;
          pickLSN = lsn;
        }
      }
      const txnId = pickTxn as TxnId;
      const record = byLSN.get(pickLSN);

      if (record === undefined || pickLSN === NULL_LSN) {
        // Chain exhausted without an explicit BEGIN: finalize defensively.
        await this.wal.append({
          type: LogRecordType.END,
          txnId,
          prevLSN: analysis.txnTable.get(txnId)?.lastLSN ?? NULL_LSN,
        });
        endWritten += 1;
        toUndo.delete(txnId);
        continue;
      }

      switch (record.type) {
        case LogRecordType.UPDATE: {
          const prev = analysis.txnTable.get(txnId)?.lastLSN ?? NULL_LSN;
          const clr = await this.wal.append({
            type: LogRecordType.CLR,
            txnId,
            prevLSN: prev,
            pageId: record.pageId,
            before: record.after,
            after: record.before,
            undoNextLSN: record.prevLSN,
          });
          byLSN.set(clr.lsn, clr);
          // Compensation is itself redo-only history: apply immediately.
          this.pool.applyLogged(record.pageId, record.before, clr.lsn);
          clrWritten += 1;
          if (record.prevLSN === NULL_LSN) {
            endWritten += await this.finishTxn(txnId, analysis, byLSN);
            toUndo.delete(txnId);
          } else {
            toUndo.set(txnId, record.prevLSN);
          }
          break;
        }
        case LogRecordType.CLR:
          // Already compensated before the crash — skip via undoNextLSN.
          if (record.undoNextLSN === NULL_LSN) {
            endWritten += await this.finishTxn(txnId, analysis, byLSN);
            toUndo.delete(txnId);
          } else {
            toUndo.set(txnId, record.undoNextLSN);
          }
          break;
        case LogRecordType.BEGIN: {
          endWritten += await this.finishTxn(txnId, analysis, byLSN);
          toUndo.delete(txnId);
          break;
        }
        case LogRecordType.ABORT:
        case LogRecordType.COMMIT:
          toUndo.set(txnId, record.prevLSN);
          break;
        default:
          toUndo.delete(txnId);
          break;
      }
    }

    return { loserTxns, winnerTxns, clrWritten, endWritten };
  }
}
