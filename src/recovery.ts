/**
 * ARIES crash-recovery engine: Analysis -> Redo -> Undo.
 *
 * - Analysis: scans the WAL from the last checkpoint, rebuilding the
 *   TransactionTable (TT) and DirtyPageTable (DPT), and identifies loser
 *   transactions (active or aborting at crash time).
 * - Redo: repeats history from the smallest recLSN in the DPT, applying
 *   update/CLR records whose LSN is newer than the page's pageLSN.
 * - Undo: rolls back loser transactions in reverse LSN order, writing a CLR
 *   per undone update and an END record per finished transaction.
 */

import { LogManager } from './wal.ts';
import type { CheckpointRecord, LogRecord, TxnStatus } from './wal.ts';
import { PageStore } from './storage.ts';

export interface TtEntry {
  status: TxnStatus;
  lastLSN: number;
}

export interface AnalysisResult {
  transactionTable: Map<number, TtEntry>;
  dirtyPageTable: Map<string, number>;
  losers: number[];
  checkpointLSN: number;
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redoneLSNs: number[];
  undoneLSNs: number[];
  clrLSNs: number[];
}

export class RecoveryManager {
  private readonly walPath: string;
  private readonly pageStore: PageStore;

  constructor(walPath: string, pageStore: PageStore) {
    this.walPath = walPath;
    this.pageStore = pageStore;
  }

  async recover(): Promise<RecoveryReport> {
    const log = await LogManager.readAll(this.walPath);
    await this.pageStore.load();

    const analysis = this.analysis(log);
    const redoneLSNs = this.redo(log, analysis);
    // Snapshot analysis output before undo mutates the transaction table.
    const analysisSnapshot: AnalysisResult = {
      transactionTable: new Map(
        [...analysis.transactionTable].map(([k, v]) => [k, { ...v }]),
      ),
      dirtyPageTable: new Map(analysis.dirtyPageTable),
      losers: [...analysis.losers],
      checkpointLSN: analysis.checkpointLSN,
    };
    const { undoneLSNs, clrLSNs } = await this.undo(analysis);

    await this.pageStore.flush();
    return { analysis: analysisSnapshot, redoneLSNs, undoneLSNs, clrLSNs };
  }

  /** Phase 1: rebuild TT and DPT starting from the most recent checkpoint. */
  analysis(log: LogRecord[]): AnalysisResult {
    let checkpoint: CheckpointRecord | undefined;
    for (const rec of log) {
      if (rec.type === 'checkpoint') checkpoint = rec;
    }

    const tt = new Map<number, TtEntry>();
    const dpt = new Map<string, number>();
    if (checkpoint) {
      for (const [txnId, e] of Object.entries(checkpoint.transactions)) {
        tt.set(Number(txnId), { status: e.status, lastLSN: e.lastLSN });
      }
      for (const [pageId, recLSN] of Object.entries(checkpoint.dirtyPages)) {
        dpt.set(pageId, recLSN);
      }
    }

    const startIdx = checkpoint ? log.findIndex((r) => r.lsn === checkpoint.lsn) + 1 : 0;
    for (let i = startIdx; i < log.length; i++) {
      const rec = log[i];
      if (rec.type === 'checkpoint') continue;
      if (rec.type === 'update' || rec.type === 'clr') {
        tt.set(rec.txnId, { status: tt.get(rec.txnId)?.status ?? 'running', lastLSN: rec.lsn });
        if (!dpt.has(rec.pageId)) dpt.set(rec.pageId, rec.lsn);
      } else if (rec.type === 'commit') {
        tt.set(rec.txnId, { status: 'committed', lastLSN: rec.lsn });
      } else if (rec.type === 'abort') {
        tt.set(rec.txnId, { status: 'aborting', lastLSN: rec.lsn });
      } else if (rec.type === 'end') {
        tt.delete(rec.txnId);
      }
    }

    const losers = [...tt.entries()]
      .filter(([, e]) => e.status !== 'committed')
      .map(([txnId]) => txnId)
      .sort((a, b) => a - b);

    return {
      transactionTable: tt,
      dirtyPageTable: dpt,
      losers,
      checkpointLSN: checkpoint?.lsn ?? 0,
    };
  }

  /** Phase 2: repeat history from the oldest recLSN in the DPT. */
  redo(log: LogRecord[], analysis: AnalysisResult): number[] {
    const dpt = analysis.dirtyPageTable;
    if (dpt.size === 0) return [];
    const redoFrom = Math.min(...dpt.values());
    const redone: number[] = [];

    for (const rec of log) {
      if (rec.lsn < redoFrom) continue;
      if (rec.type !== 'update' && rec.type !== 'clr') continue;
      const recLSN = dpt.get(rec.pageId);
      if (recLSN === undefined || recLSN > rec.lsn) continue; // page not dirty at rec.lsn
      const page = this.pageStore.get(rec.pageId);
      if (page && page.pageLSN >= rec.lsn) continue; // already persisted
      this.pageStore.apply(rec.pageId, rec.after, rec.lsn);
      redone.push(rec.lsn);
    }
    return redone;
  }

  /** Phase 3: undo losers in reverse LSN order, emitting CLRs and ENDs. */
  private async undo(
    analysis: AnalysisResult,
  ): Promise<{ undoneLSNs: number[]; clrLSNs: number[] }> {
    const log = await LogManager.readAll(this.walPath);
    const byLSN = new Map(log.map((r) => [r.lsn, r]));
    // Continue the LSN sequence right after the crashed WAL's last record.
    const logMgr = new LogManager(this.walPath, (log.at(-1)?.lsn ?? 0) + 1);

    // Max-heap (by LSN) of loser records still to process.
    const pending: number[] = analysis.losers
      .map((txnId) => analysis.transactionTable.get(txnId)!.lastLSN)
      .filter((lsn) => lsn > 0);
    const undone: number[] = [];
    const clrs: number[] = [];

    while (pending.length > 0) {
      pending.sort((a, b) => b - a);
      const lsn = pending.shift()!;
      const rec = byLSN.get(lsn);
      if (!rec) continue;

      if (rec.type === 'update') {
        const clr = logMgr.append({
          type: 'clr',
          txnId: rec.txnId,
          pageId: rec.pageId,
          prevLSN: analysis.transactionTable.get(rec.txnId)?.lastLSN ?? 0,
          undoNextLSN: rec.prevLSN,
          before: rec.after,
          after: rec.before,
        });
        // A CLR is itself a redoable log record: apply the undo to the page.
        this.pageStore.apply(rec.pageId, rec.before, clr.lsn);
        analysis.transactionTable.set(rec.txnId, { status: 'aborting', lastLSN: clr.lsn });
        undone.push(rec.lsn);
        clrs.push(clr.lsn);
        if (rec.prevLSN > 0) pending.push(rec.prevLSN);
      } else if (rec.type === 'clr') {
        if (rec.undoNextLSN > 0) pending.push(rec.undoNextLSN);
      } else if (rec.type === 'commit' || rec.type === 'abort') {
        if (rec.prevLSN > 0) pending.push(rec.prevLSN);
      }

      const entry = analysis.transactionTable.get(rec.txnId);
      const next = rec.type === 'clr' ? rec.undoNextLSN : rec.type === 'update' ? rec.prevLSN : 0;
      if (rec.type !== 'end' && next === 0 && (rec.type === 'update' || rec.type === 'clr')) {
        // Transaction fully undone: write END.
        const end = logMgr.append({
          type: 'end',
          txnId: rec.txnId,
          prevLSN: entry?.lastLSN ?? 0,
        });
        analysis.transactionTable.delete(rec.txnId);
        void end;
      }
    }

    await logMgr.flush();
    return { undoneLSNs: undone, clrLSNs: clrs };
  }
}
