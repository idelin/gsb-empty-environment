import { LogType, TxnStatus } from './types.ts';
import type {
  DirtyPageTable,
  EndCheckpointRecord,
  LSN,
  LogRecord,
  PageId,
  TxnId,
  TxnTableEntry,
} from './types.ts';
import { WriteAheadLog } from './wal.ts';
import type { BufferPool } from './storage.ts';

export interface AnalysisResult {
  checkpointLSN: LSN | null;
  txnTable: TxnTableEntry[];
  dirtyPages: { pageId: PageId; recLSN: LSN }[];
  redoLSN: LSN;
}

export interface RedoResult {
  applied: LSN[];
  skipped: LSN[];
}

export interface UndoResult {
  losers: TxnId[];
  clrs: LSN[];
  ended: TxnId[];
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redo: RedoResult;
  undo: UndoResult;
}

/** Thrown by the `crashAfterClrs` test hook to simulate a crash mid-undo. */
export class SimulatedCrashError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SimulatedCrashError';
  }
}

/**
 * ARIES recovery manager: analysis -> redo (repeat history) -> undo (losers,
 * logging CLRs). Idempotent: safe to re-run after a crash during recovery.
 */
export class RecoveryManager {
  private readonly wal: WriteAheadLog;
  private readonly pool: BufferPool;
  /** Test hook: abort undo after writing this many CLRs. */
  crashAfterClrs: number | null = null;

  constructor(wal: WriteAheadLog, pool: BufferPool) {
    this.wal = wal;
    this.pool = pool;
  }

  recover(): RecoveryReport {
    const records = WriteAheadLog.read(this.wal.filePath);
    const analysis = this.analysis(records);
    const dpt: DirtyPageTable = new Map(
      analysis.dirtyPages.map((d) => [d.pageId, d.recLSN]),
    );
    const redo = this.redo(records, dpt, analysis.redoLSN);
    const undo = this.undo(records, analysis.txnTable);
    this.wal.flush();
    this.pool.flushAll();
    return { analysis, redo, undo };
  }

  /**
   * Phase 1 — locate the most recent END_CHECKPOINT, seed the transaction
   * table and dirty-page table from it, then scan forward to the end of the
   * log tracking transaction state and first-dirtying LSNs.
   */
  analysis(records: LogRecord[]): AnalysisResult {
    let checkpointIndex = -1;
    for (let i = records.length - 1; i >= 0; i--) {
      if (records[i].type === LogType.END_CHECKPOINT) {
        checkpointIndex = i;
        break;
      }
    }
    const txnTable = new Map<TxnId, TxnTableEntry>();
    const dpt: DirtyPageTable = new Map();
    let checkpointLSN: LSN | null = null;
    let start = 0;
    if (checkpointIndex >= 0) {
      const checkpoint = records[checkpointIndex] as EndCheckpointRecord;
      checkpointLSN = checkpoint.lsn;
      for (const entry of checkpoint.txnTable) {
        txnTable.set(entry.txnId, { ...entry });
      }
      for (const dirty of checkpoint.dirtyPages) {
        dpt.set(dirty.pageId, dirty.recLSN);
      }
      start = checkpointIndex + 1;
    }
    for (let i = start; i < records.length; i++) {
      const record = records[i];
      switch (record.type) {
        case LogType.BEGIN:
          txnTable.set(record.txnId, {
            txnId: record.txnId,
            status: TxnStatus.RUNNING,
            lastLSN: record.lsn,
          });
          break;
        case LogType.UPDATE:
        case LogType.CLR: {
          const entry = txnTable.get(record.txnId);
          if (entry) {
            entry.lastLSN = record.lsn;
          } else {
            txnTable.set(record.txnId, {
              txnId: record.txnId,
              status: TxnStatus.RUNNING,
              lastLSN: record.lsn,
            });
          }
          if (!dpt.has(record.pageId)) dpt.set(record.pageId, record.lsn);
          break;
        }
        case LogType.COMMIT: {
          const entry = txnTable.get(record.txnId);
          if (entry) {
            entry.status = TxnStatus.COMMITTING;
            entry.lastLSN = record.lsn;
          }
          break;
        }
        case LogType.ABORT: {
          const entry = txnTable.get(record.txnId);
          if (entry) {
            entry.status = TxnStatus.ABORTING;
            entry.lastLSN = record.lsn;
          }
          break;
        }
        case LogType.END:
          txnTable.delete(record.txnId);
          break;
        default:
          break;
      }
    }
    const lastLSN = records.length === 0 ? 0 : records[records.length - 1].lsn;
    let redoLSN = lastLSN + 1;
    for (const recLSN of dpt.values()) {
      if (recLSN < redoLSN) redoLSN = recLSN;
    }
    return {
      checkpointLSN,
      txnTable: [...txnTable.values()].map((e) => ({ ...e })),
      dirtyPages: [...dpt.entries()]
        .map(([pageId, recLSN]) => ({ pageId, recLSN }))
        .sort((a, b) => a.pageId - b.pageId),
      redoLSN,
    };
  }

  /**
   * Phase 2 — repeat history: reapply every logged update/CLR whose page is
   * dirty in the DPT and stale on disk (pageLSN < record LSN).
   */
  redo(records: LogRecord[], dpt: DirtyPageTable, redoLSN: LSN): RedoResult {
    const applied: LSN[] = [];
    const skipped: LSN[] = [];
    for (const record of records) {
      if (record.lsn < redoLSN) continue;
      if (record.type !== LogType.UPDATE && record.type !== LogType.CLR) continue;
      const recLSN = dpt.get(record.pageId);
      if (recLSN === undefined || recLSN > record.lsn) {
        skipped.push(record.lsn);
        continue;
      }
      const page = this.pool.get(record.pageId);
      if (page.pageLSN >= record.lsn) {
        skipped.push(record.lsn);
        continue;
      }
      this.pool.updatePage(record.pageId, record.after, record.lsn);
      applied.push(record.lsn);
    }
    return { applied, skipped };
  }

  /**
   * Phase 3 — roll back every loser transaction in reverse LSN order
   * (largest first across all losers), writing a CLR per undone update and
   * an END record once a transaction is fully undone.
   */
  undo(records: LogRecord[], txnTable: TxnTableEntry[]): UndoResult {
    const toUndo = new Map<TxnId, LSN>();
    const lastNewLSN = new Map<TxnId, LSN>();
    const clrs: LSN[] = [];
    const ended: TxnId[] = [];
    for (const entry of txnTable) {
      if (entry.status === TxnStatus.COMMITTING) {
        // The commit record reached stable storage: the txn is committed,
        // only its END record was lost.
        this.wal.append({
          type: LogType.END,
          txnId: entry.txnId,
          prevLSN: entry.lastLSN,
        });
        ended.push(entry.txnId);
      } else {
        toUndo.set(entry.txnId, entry.lastLSN);
        lastNewLSN.set(entry.txnId, entry.lastLSN);
      }
    }
    const losers = [...toUndo.keys()].sort((a, b) => a - b);
    while (toUndo.size > 0) {
      let pickTxn: TxnId | null = null;
      let pickLSN: LSN = -1;
      for (const [txnId, lsn] of toUndo) {
        if (lsn > pickLSN) {
          pickLSN = lsn;
          pickTxn = txnId;
        }
      }
      const txnId = pickTxn!;
      const record = records[pickLSN - 1];
      if (!record || record.lsn !== pickLSN) {
        throw new Error(`WAL corrupt: no record at LSN ${pickLSN}`);
      }
      let next: LSN | null;
      if (record.type === LogType.UPDATE) {
        const clr = this.wal.append({
          type: LogType.CLR,
          txnId,
          prevLSN: lastNewLSN.get(txnId)!,
          pageId: record.pageId,
          before: record.after,
          after: record.before,
          undoNextLSN: record.prevLSN,
        });
        this.pool.updatePage(record.pageId, record.before, clr.lsn);
        clrs.push(clr.lsn);
        lastNewLSN.set(txnId, clr.lsn);
        if (this.crashAfterClrs !== null && clrs.length >= this.crashAfterClrs) {
          this.wal.flush();
          throw new SimulatedCrashError(
            `simulated crash during undo after ${clrs.length} CLR(s)`,
          );
        }
        next = record.prevLSN;
      } else if (record.type === LogType.CLR) {
        next = record.undoNextLSN;
      } else {
        next = record.prevLSN; // BEGIN / ABORT / ... — skip along the chain
      }
      if (next === null) {
        this.wal.append({
          type: LogType.END,
          txnId,
          prevLSN: lastNewLSN.get(txnId)!,
        });
        toUndo.delete(txnId);
        ended.push(txnId);
      } else {
        toUndo.set(txnId, next);
      }
    }
    return { losers, clrs, ended };
  }
}
