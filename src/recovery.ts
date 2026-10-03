import type { LSN, PageId, TTEntry, TxnId } from './types.ts';
import type { LogManager } from './wal.ts';
import type { BufferPool } from './storage.ts';

export interface AnalysisResult {
  tt: Map<TxnId, TTEntry>;
  dpt: Map<PageId, LSN>;
  redoLSN: LSN;
  checkpointBeginLSN: LSN | null;
  walLength: number;
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redoApplied: number;
  redoSkipped: number;
  losers: TxnId[];
  winners: TxnId[];
  clrsWritten: number;
  endsWritten: number;
}

// ARIES crash recovery: Analysis -> Redo (repeat history) -> Undo (CLRs).
export class RecoveryManager {
  private log: LogManager;
  private bp: BufferPool;
  private walLength: number;

  constructor(log: LogManager, bp: BufferPool) {
    this.log = log;
    this.bp = bp;
    this.walLength = log.durableCount;
  }

  recover(): RecoveryReport {
    const analysis = this.analysis();
    const redo = this.redo(analysis);
    const undo = this.undo(analysis);
    this.bp.flushAll();
    this.log.flush();
    return {
      analysis,
      redoApplied: redo.applied,
      redoSkipped: redo.skipped,
      losers: undo.losers,
      winners: undo.winners,
      clrsWritten: undo.clrs,
      endsWritten: undo.ends,
    };
  }

  // Phase 1: rebuild TransactionTable and DirtyPageTable, starting from the
  // most recent completed fuzzy checkpoint when one exists.
  analysis(): AnalysisResult {
    const tt = new Map<TxnId, TTEntry>();
    const dpt = new Map<PageId, LSN>();
    let startLSN = 0;
    let checkpointBeginLSN: LSN | null = null;

    for (let lsn = this.walLength - 1; lsn >= 0; lsn--) {
      const record = this.log.read(lsn);
      if (record.type === 'END_CHECKPOINT') {
        checkpointBeginLSN = record.beginLSN;
        startLSN = record.beginLSN;
        for (const entry of record.transactionTable) tt.set(entry.txnId, { ...entry });
        for (const d of record.dirtyPageTable) dpt.set(d.pageId, d.recLSN);
        break;
      }
    }

    for (let lsn = startLSN; lsn < this.walLength; lsn++) {
      const record = this.log.read(lsn);
      switch (record.type) {
        case 'UPDATE':
        case 'CLR': {
          const entry = this.ensureTxn(tt, record.txnId);
          entry.lastLSN = record.lsn;
          if (!dpt.has(record.pageId)) dpt.set(record.pageId, record.lsn);
          break;
        }
        case 'COMMIT': {
          const entry = this.ensureTxn(tt, record.txnId);
          entry.status = 'COMMITTED';
          entry.lastLSN = record.lsn;
          break;
        }
        case 'ABORT': {
          const entry = this.ensureTxn(tt, record.txnId);
          entry.status = 'ABORTING';
          entry.lastLSN = record.lsn;
          break;
        }
        case 'END':
          tt.delete(record.txnId);
          break;
        default:
          break;
      }
    }

    let redoLSN = this.walLength;
    for (const recLSN of dpt.values()) if (recLSN < redoLSN) redoLSN = recLSN;
    return { tt, dpt, redoLSN, checkpointBeginLSN, walLength: this.walLength };
  }

  // Phase 2: repeat history. Reapply every UPDATE/CLR affecting a dirty page
  // unless the on-disk pageLSN proves the update already reached disk.
  redo(analysis: AnalysisResult): { applied: number; skipped: number } {
    let applied = 0;
    let skipped = 0;
    for (let lsn = analysis.redoLSN; lsn < this.walLength; lsn++) {
      const record = this.log.read(lsn);
      if (record.type !== 'UPDATE' && record.type !== 'CLR') continue;
      const recLSN = analysis.dpt.get(record.pageId);
      if (recLSN === undefined || record.lsn < recLSN) {
        skipped++;
        continue;
      }
      const page = this.bp.fetch(record.pageId);
      if (page.pageLSN >= record.lsn) {
        skipped++;
        continue;
      }
      this.bp.update(record.pageId, record.key, record.after, record.lsn);
      applied++;
    }
    return { applied, skipped };
  }

  // Phase 3: roll back all loser transactions in reverse LSN order, writing a
  // CLR per undone update. CLRs are never undone; undoNextLSN skips work
  // already compensated before the crash.
  undo(analysis: AnalysisResult): { losers: TxnId[]; winners: TxnId[]; clrs: number; ends: number } {
    const losers: TxnId[] = [];
    const winners: TxnId[] = [];
    const toUndo = new Set<LSN>();
    for (const entry of analysis.tt.values()) {
      if (entry.status === 'COMMITTED') {
        winners.push(entry.txnId);
        continue;
      }
      losers.push(entry.txnId);
      if (entry.lastLSN !== null) toUndo.add(entry.lastLSN);
    }

    let clrs = 0;
    let ends = 0;
    const txnLastLSN = new Map<TxnId, LSN | null>();
    for (const entry of analysis.tt.values()) txnLastLSN.set(entry.txnId, entry.lastLSN);
    const writeEnd = (txnId: TxnId): void => {
      this.log.append({ lsn: -1, type: 'END', txnId, prevLSN: txnLastLSN.get(txnId) ?? null });
      ends++;
    };
    // Losers that never logged anything still get an END so the WAL is closed.
    for (const entry of analysis.tt.values()) {
      if (entry.status !== 'COMMITTED' && entry.lastLSN === null) writeEnd(entry.txnId);
    }

    while (toUndo.size > 0) {
      let maxLSN = -1;
      for (const lsn of toUndo) if (lsn > maxLSN) maxLSN = lsn;
      toUndo.delete(maxLSN);
      const record = this.log.read(maxLSN);

      if (record.type === 'UPDATE') {
        const clr = this.log.append({
          lsn: -1,
          type: 'CLR',
          txnId: record.txnId,
          prevLSN: txnLastLSN.get(record.txnId) ?? record.lsn,
          pageId: record.pageId,
          key: record.key,
          before: record.after,
          after: record.before,
          undoNextLSN: record.prevLSN,
        });
        txnLastLSN.set(record.txnId, clr.lsn);
        this.bp.update(record.pageId, record.key, record.before, clr.lsn);
        clrs++;
        if (record.prevLSN !== null) toUndo.add(record.prevLSN);
        else writeEnd(record.txnId);
      } else if (record.type === 'CLR') {
        if (record.undoNextLSN !== null) toUndo.add(record.undoNextLSN);
        else writeEnd(record.txnId);
      } else if (record.type === 'ABORT' || record.type === 'COMMIT') {
        if (record.prevLSN !== null) toUndo.add(record.prevLSN);
        else writeEnd(record.txnId);
      }
    }
    losers.sort((a, b) => a - b);
    winners.sort((a, b) => a - b);
    return { losers, winners, clrs, ends };
  }

  private ensureTxn(tt: Map<TxnId, TTEntry>, txnId: TxnId): TTEntry {
    let entry = tt.get(txnId);
    if (!entry) {
      entry = { txnId, status: 'RUNNING', lastLSN: null };
      tt.set(txnId, entry);
    }
    return entry;
  }
}
