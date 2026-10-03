import type { LogRecord, LSN, TxnStatus } from './log-record.ts';
import { LogManager } from './log-manager.ts';
import type { StorageEngine } from './storage.ts';

export interface TxnTableEntry {
  lastLSN: LSN;
  status: TxnStatus;
}

export interface AnalysisResult {
  /** Transactions still "active" at crash time (END not yet written). */
  txnTable: Map<number, TxnTableEntry>;
  /** pageId -> recLSN: oldest possibly-unpersisted update per page. */
  dirtyPageTable: Map<number, LSN>;
  /** LSN of the checkpoint the scan started from (0 = none found). */
  checkpointLSN: LSN;
}

export interface UndoResult {
  losers: number[];
  clrLSNs: LSN[];
  endLSNs: LSN[];
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  winners: number[];
  redoneLSNs: LSN[];
  undo: UndoResult;
}

/**
 * ARIES crash recovery: Analysis reconstructs the transaction table and
 * DirtyPageTable as of the crash; Redo repeats history from the oldest
 * recLSN; Undo rolls back every loser transaction in reverse LSN order,
 * logging a CLR for each compensated update.
 */
export class AriesRecovery {
  private readonly log: LogManager;
  private readonly storage: StorageEngine;

  constructor(log: LogManager, storage: StorageEngine) {
    this.log = log;
    this.storage = storage;
  }

  /** Full restart recovery from a WAL file. */
  recover(walPath: string): RecoveryReport {
    const records = LogManager.readFile(walPath);
    this.log.loadRecords(records);
    const analysis = AriesRecovery.analyze(records);
    const redoneLSNs = this.redo(records, analysis);
    const undo = this.undo(analysis);
    this.log.flush();
    this.storage.flushAll();
    const winners: number[] = [];
    for (const [txnId, entry] of analysis.txnTable) {
      if (entry.status === 'COMMITTING') winners.push(txnId);
    }
    return { analysis, winners, redoneLSNs, undo };
  }

  /**
   * Phase 1 — Analysis. Scan forward from the most recent checkpoint
   * (merging its snapshot) to end-of-log, tracking every transaction's
   * lastLSN/status and the first LSN that dirtied each page.
   */
  static analyze(records: LogRecord[]): AnalysisResult {
    const txnTable = new Map<number, TxnTableEntry>();
    const dirtyPageTable = new Map<number, LSN>();
    let startIndex = 0;
    let checkpointLSN: LSN = 0;

    for (let i = records.length - 1; i >= 0; i--) {
      if (records[i].type === 'CHECKPOINT') {
        startIndex = i;
        checkpointLSN = records[i].lsn;
        const cp = records[i].checkpoint!;
        for (const [id, info] of Object.entries(cp.activeTxns)) {
          txnTable.set(Number(id), { lastLSN: info.lastLSN, status: info.status });
        }
        for (const [pageId, recLSN] of Object.entries(cp.dirtyPages)) {
          dirtyPageTable.set(Number(pageId), recLSN);
        }
        break;
      }
    }

    const touch = (txnId: number): TxnTableEntry => {
      let entry = txnTable.get(txnId);
      if (!entry) {
        entry = { lastLSN: 0, status: 'RUNNING' };
        txnTable.set(txnId, entry);
      }
      return entry;
    };

    for (let i = startIndex; i < records.length; i++) {
      const rec = records[i];
      switch (rec.type) {
        case 'BEGIN':
          touch(rec.txnId).lastLSN = rec.lsn;
          break;
        case 'UPDATE':
        case 'CLR': {
          touch(rec.txnId).lastLSN = rec.lsn;
          if (rec.pageId !== undefined && !dirtyPageTable.has(rec.pageId)) {
            dirtyPageTable.set(rec.pageId, rec.lsn);
          }
          break;
        }
        case 'COMMIT': {
          const entry = touch(rec.txnId);
          entry.lastLSN = rec.lsn;
          entry.status = 'COMMITTING';
          break;
        }
        case 'ABORT': {
          const entry = touch(rec.txnId);
          entry.lastLSN = rec.lsn;
          entry.status = 'ABORTING';
          break;
        }
        case 'END':
          txnTable.delete(rec.txnId);
          break;
        case 'CHECKPOINT': {
          const cp = rec.checkpoint!;
          for (const [id, info] of Object.entries(cp.activeTxns)) {
            if (!txnTable.has(Number(id))) {
              txnTable.set(Number(id), {
                lastLSN: info.lastLSN,
                status: info.status,
              });
            }
          }
          for (const [pageId, recLSN] of Object.entries(cp.dirtyPages)) {
            if (!dirtyPageTable.has(Number(pageId))) {
              dirtyPageTable.set(Number(pageId), recLSN);
            }
          }
          break;
        }
      }
    }
    return { txnTable, dirtyPageTable, checkpointLSN };
  }

  /**
   * Phase 2 — Redo. Repeat history from the smallest recLSN: reapply the
   * after-image of every UPDATE/CLR whose page may not reflect it. A
   * record is redone only if its page is in the DPT, the record is not
   * older than the page's recLSN, and the page's pageLSN predates it.
   */
  redo(records: LogRecord[], analysis: AnalysisResult): LSN[] {
    const { dirtyPageTable } = analysis;
    if (dirtyPageTable.size === 0) return [];
    const startLSN = Math.min(...dirtyPageTable.values());
    const redone: LSN[] = [];
    for (const rec of records) {
      if (rec.lsn < startLSN) continue;
      if (rec.type !== 'UPDATE' && rec.type !== 'CLR') continue;
      const pageId = rec.pageId!;
      const recLSN = dirtyPageTable.get(pageId);
      if (recLSN === undefined || rec.lsn < recLSN) continue;
      const page = this.storage.fetchPage(pageId);
      if (page.pageLSN >= rec.lsn) continue;
      this.storage.writeSlot(pageId, rec.offset!, rec.afterImage!, rec.lsn);
      redone.push(rec.lsn);
    }
    return redone;
  }

  /**
   * Phase 3 — Undo. Roll back every loser (RUNNING/ABORTING at crash) by
   * walking the union of their prevLSN chains in globally descending LSN
   * order. Each undone UPDATE is compensated with a CLR whose undoNextLSN
   * points past already-undone work, so a crash during Undo never
   * re-undoes anything. A txn is finalized with END at the end of its
   * chain.
   */
  undo(analysis: AnalysisResult): UndoResult {
    const losers = new Map<number, LSN>();
    for (const [txnId, entry] of analysis.txnTable) {
      if (entry.status !== 'COMMITTING') losers.set(txnId, entry.lastLSN);
    }
    const clrLSNs: LSN[] = [];
    const endLSNs: LSN[] = [];
    const toUndo: LSN[] = [...losers.values()];

    const finishTxn = (txnId: number): void => {
      const end = this.log.append({
        type: 'END',
        txnId,
        prevLSN: losers.get(txnId)!,
      });
      endLSNs.push(end.lsn);
      losers.delete(txnId);
    };

    while (toUndo.length > 0) {
      // Global max-LSN-first ordering across all loser chains.
      let maxIdx = 0;
      for (let i = 1; i < toUndo.length; i++) {
        if (toUndo[i] > toUndo[maxIdx]) maxIdx = i;
      }
      const lsn = toUndo.splice(maxIdx, 1)[0];
      const rec = this.log.getRecord(lsn);
      const txnId = rec.txnId;

      if (rec.type === 'UPDATE') {
        const clr = this.log.append({
          type: 'CLR',
          txnId,
          prevLSN: losers.get(txnId)!,
          pageId: rec.pageId,
          offset: rec.offset,
          beforeImage: rec.afterImage,
          afterImage: rec.beforeImage,
          undoNextLSN: rec.prevLSN,
        });
        losers.set(txnId, clr.lsn);
        clrLSNs.push(clr.lsn);
        this.storage.writeSlot(
          rec.pageId!,
          rec.offset!,
          rec.beforeImage!,
          clr.lsn,
        );
        if (rec.prevLSN !== 0) toUndo.push(rec.prevLSN);
        else finishTxn(txnId);
      } else if (rec.type === 'CLR') {
        const next = rec.undoNextLSN ?? 0;
        if (next !== 0) toUndo.push(next);
        else finishTxn(txnId);
      } else {
        // BEGIN / ABORT (and any other non-update record): just follow
        // the chain; nothing to compensate.
        if (rec.prevLSN !== 0) toUndo.push(rec.prevLSN);
        else finishTxn(txnId);
      }
    }
    return {
      losers: [...analysis.txnTable]
        .filter(([, e]) => e.status !== 'COMMITTING')
        .map(([id]) => id)
        .sort((a, b) => a - b),
      clrLSNs,
      endLSNs,
    };
  }
}
