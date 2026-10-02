/**
 * ARIES crash recovery: Analysis -> Redo (repeat history) -> Undo (with CLRs).
 *
 * Follows the classic protocol from Mohan et al.:
 *  - Analysis rebuilds the transaction table and dirty page table starting
 *    from the most recent checkpoint.
 *  - Redo repeats history from the smallest recLSN, using pageLSN checks so
 *    the pass is idempotent.
 *  - Undo rolls back every loser transaction in reverse LSN order, writing a
 *    compensation log record (CLR) per undone update; CLRs carry
 *    undoNextLSN so a crash during undo never undoes the same record twice.
 */

import type { DiskManager } from "./storage.ts";
import type { WalManager } from "./wal.ts";
import type {
  AnalysisResult,
  CheckpointRecord,
  LogRecord,
  LSN,
  TxnId,
} from "./types.ts";
import { LogType, TxnStatus } from "./types.ts";

export interface RecoveryReport {
  analysis: AnalysisResult;
  redoneLSNs: LSN[];
  undoneLSNs: LSN[];
  clrLSNs: LSN[];
  endedTxns: TxnId[];
}

export class AriesRecovery {
  private readonly wal: WalManager;
  private readonly disk: DiskManager;

  constructor(wal: WalManager, disk: DiskManager) {
    this.wal = wal;
    this.disk = disk;
  }

  /** Phase 1: rebuild transaction table + dirty page table. */
  analysis(): AnalysisResult {
    const transactionTable: AnalysisResult["transactionTable"] = new Map();
    const dirtyPageTable: AnalysisResult["dirtyPageTable"] = new Map();

    const records = this.wal.all();
    let startIndex = 0;

    // Locate the most recent checkpoint and seed both tables from it.
    for (let i = records.length - 1; i >= 0; i--) {
      const record = records[i];
      if (record.type === LogType.CHECKPOINT) {
        const checkpoint = record as CheckpointRecord;
        for (const [txnId, status, lastLSN] of checkpoint.att) {
          transactionTable.set(txnId, { txnId, status, lastLSN });
        }
        for (const [pageId, recLSN] of checkpoint.dpt) {
          dirtyPageTable.set(pageId, { pageId, recLSN });
        }
        startIndex = i + 1;
        break;
      }
    }

    // Scan forward from the checkpoint to the end of the log.
    for (let i = startIndex; i < records.length; i++) {
      const record = records[i];
      switch (record.type) {
        case LogType.BEGIN:
          transactionTable.set(record.txnId, {
            txnId: record.txnId,
            status: TxnStatus.RUNNING,
            lastLSN: record.lsn,
          });
          break;
        case LogType.UPDATE:
        case LogType.CLR: {
          const entry = transactionTable.get(record.txnId);
          if (entry) {
            entry.status = TxnStatus.RUNNING;
            entry.lastLSN = record.lsn;
          } else {
            transactionTable.set(record.txnId, {
              txnId: record.txnId,
              status: TxnStatus.RUNNING,
              lastLSN: record.lsn,
            });
          }
          if (record.pageId !== undefined && !dirtyPageTable.has(record.pageId)) {
            dirtyPageTable.set(record.pageId, {
              pageId: record.pageId,
              recLSN: record.lsn,
            });
          }
          break;
        }
        case LogType.COMMIT: {
          const entry = transactionTable.get(record.txnId);
          if (entry) {
            entry.status = TxnStatus.COMMITTED;
            entry.lastLSN = record.lsn;
          }
          break;
        }
        case LogType.ABORT: {
          const entry = transactionTable.get(record.txnId);
          if (entry) {
            entry.status = TxnStatus.ABORTING;
            entry.lastLSN = record.lsn;
          }
          break;
        }
        case LogType.END:
          transactionTable.delete(record.txnId);
          break;
      }
    }

    let redoStartLSN: LSN = Number.POSITIVE_INFINITY;
    for (const entry of dirtyPageTable.values()) {
      redoStartLSN = Math.min(redoStartLSN, entry.recLSN);
    }
    if (!Number.isFinite(redoStartLSN)) redoStartLSN = this.wal.maxLSN + 1;

    return { transactionTable, dirtyPageTable, redoStartLSN };
  }

  /** Phase 2: repeat history. Idempotent via pageLSN comparison. */
  redo(analysis: AnalysisResult): LSN[] {
    const redone: LSN[] = [];
    for (const record of this.wal.scanFrom(analysis.redoStartLSN)) {
      if (record.type !== LogType.UPDATE && record.type !== LogType.CLR) continue;
      if (record.pageId === undefined || record.afterImage === undefined) continue;

      const dptEntry = analysis.dirtyPageTable.get(record.pageId);
      if (!dptEntry) continue; // page was clean at crash time
      if (record.lsn < dptEntry.recLSN) continue; // predates first dirtying

      const page = this.disk.readPage(record.pageId);
      if (page.pageLSN >= record.lsn) continue; // already on disk

      this.disk.writePage(record.pageId, record.afterImage, record.lsn);
      redone.push(record.lsn);
    }
    return redone;
  }

  /**
   * Phase 3: roll back every loser transaction in reverse LSN order,
   * emitting one CLR per undone update and an END record per transaction.
   */
  undo(analysis: AnalysisResult): { undoneLSNs: LSN[]; clrLSNs: LSN[]; endedTxns: TxnId[] } {
    const undoneLSNs: LSN[] = [];
    const clrLSNs: LSN[] = [];
    const endedTxns: TxnId[] = [];

    // Priority queue (max-LSN first) of records still to process.
    const toUndo: Array<{ lsn: LSN; txnId: TxnId }> = [];
    for (const entry of analysis.transactionTable.values()) {
      if (entry.status === TxnStatus.COMMITTED) continue; // winner
      toUndo.push({ lsn: entry.lastLSN, txnId: entry.txnId });
    }

    while (toUndo.length > 0) {
      toUndo.sort((a, b) => b.lsn - a.lsn);
      const { lsn, txnId } = toUndo.shift()!;
      const record = this.wal.getRecord(lsn);
      if (!record) continue;

      if (record.type === LogType.UPDATE) {
        // Write a CLR and physically undo the update on disk.
        const clr = this.wal.append({
          type: LogType.CLR,
          txnId,
          prevLSN: lsn,
          pageId: record.pageId,
          beforeImage: record.afterImage,
          afterImage: record.beforeImage,
          undoNextLSN: record.prevLSN,
        });
        clrLSNs.push(clr.lsn);
        undoneLSNs.push(lsn);
        if (record.pageId !== undefined && record.beforeImage !== undefined) {
          this.disk.writePage(record.pageId, record.beforeImage, clr.lsn);
        }
        this.followChain(record.prevLSN, txnId, toUndo);
      } else if (record.type === LogType.CLR) {
        // Already compensated: jump straight to undoNextLSN.
        this.followChain(record.undoNextLSN ?? 0, txnId, toUndo);
      } else {
        // BEGIN / ABORT / anything else: keep walking the chain.
        this.followChain(record.prevLSN, txnId, toUndo);
      }
    }

    return { undoneLSNs, clrLSNs, endedTxns };
  }

  private followChain(
    nextLSN: LSN,
    txnId: TxnId,
    toUndo: Array<{ lsn: LSN; txnId: TxnId }>,
  ): void {
    if (nextLSN > 0) {
      toUndo.push({ lsn: nextLSN, txnId });
    } else {
      // Reached the start of the transaction: it is fully rolled back.
      this.wal.append({ type: LogType.END, txnId, prevLSN: 0 });
    }
  }

  /** Run the full three-phase restart recovery. */
  recover(): RecoveryReport {
    const analysis = this.analysis();
    const redoneLSNs = this.redo(analysis);
    const { undoneLSNs, clrLSNs } = this.undo(analysis);
    const endedTxns: TxnId[] = [];
    for (const entry of analysis.transactionTable.values()) {
      if (entry.status !== TxnStatus.COMMITTED) endedTxns.push(entry.txnId);
    }
    return { analysis, redoneLSNs, undoneLSNs, clrLSNs, endedTxns };
  }
}
