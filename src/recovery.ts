/**
 * ARIES crash recovery: Analysis -> Redo (repeat history) -> Undo (with CLRs).
 *
 * - Analysis: locate the last checkpoint, seed the transaction table and the
 *   dirty page table from it, then scan forward to the end of the log.
 * - Redo: replay every UPDATE/CLR from the smallest recLSN in the DPT,
 *   skipping pages whose pageLSN already covers the record.
 * - Undo: roll back all loser transactions in reverse LSN order, writing a
 *   CLR per undone update and an END record per finished transaction.
 */

import type { CheckpointRecord, LogRecord, LSN, PageId, TxnId, TxnStatus } from './log-record.ts';
import type { Storage } from './storage.ts';
import type { Wal } from './wal.ts';

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export type TxnTable = Map<TxnId, TxnTableEntry>;
/** pageId -> recLSN (oldest LSN that may have dirtied the page) */
export type DirtyPageTable = Map<PageId, LSN>;

export interface AnalysisResult {
  checkpointLSN: LSN | null;
  txnTable: TxnTable;
  dirtyPageTable: DirtyPageTable;
}

export interface UndoResult {
  clrLSNs: LSN[];
  endedTxns: TxnId[];
}

export interface RecoveryReport {
  analysis: AnalysisResult;
  redoApplied: LSN[];
  undo: UndoResult;
}

export class RecoveryEngine {
  private readonly wal: Wal;
  private readonly storage: Storage;

  constructor(wal: Wal, storage: Storage) {
    this.wal = wal;
    this.storage = storage;
  }

  analyze(records: LogRecord[]): AnalysisResult {
    let checkpoint: CheckpointRecord | null = null;
    let checkpointIndex = -1;
    records.forEach((r, i) => {
      if (r.kind === 'CHECKPOINT') {
        checkpoint = r;
        checkpointIndex = i;
      }
    });

    const txnTable: TxnTable = new Map();
    const dirtyPageTable: DirtyPageTable = new Map();
    if (checkpoint !== null) {
      const cp: CheckpointRecord = checkpoint;
      for (const e of cp.txnTable) txnTable.set(e.txnId, { ...e });
      for (const e of cp.dirtyPageTable) dirtyPageTable.set(e.pageId, e.recLSN);
    }

    for (let i = checkpointIndex + 1; i < records.length; i++) {
      const r = records[i];
      switch (r.kind) {
        case 'BEGIN':
          txnTable.set(r.txnId, { txnId: r.txnId, status: 'running', lastLSN: r.lsn });
          break;
        case 'UPDATE':
        case 'CLR': {
          const entry = txnTable.get(r.txnId) ?? { txnId: r.txnId, status: 'running' as TxnStatus, lastLSN: r.lsn };
          entry.lastLSN = r.lsn;
          txnTable.set(r.txnId, entry);
          if (!dirtyPageTable.has(r.pageId)) dirtyPageTable.set(r.pageId, r.lsn);
          break;
        }
        case 'COMMIT':
        case 'ABORT': {
          const entry = txnTable.get(r.txnId);
          if (entry) {
            entry.status = r.kind === 'COMMIT' ? 'committed' : 'aborted';
            entry.lastLSN = r.lsn;
          }
          break;
        }
        case 'END':
          txnTable.delete(r.txnId);
          break;
        case 'CHECKPOINT':
          break;
      }
    }
    return { checkpointLSN: checkpoint === null ? null : (checkpoint as CheckpointRecord).lsn, txnTable, dirtyPageTable };
  }

  /** Repeat history. Returns the LSNs that were actually reapplied. */
  redo(records: LogRecord[], dirtyPageTable: DirtyPageTable): LSN[] {
    const applied: LSN[] = [];
    if (dirtyPageTable.size === 0) return applied;
    const startLSN = Math.min(...dirtyPageTable.values());
    for (const r of records) {
      if (r.lsn < startLSN) continue;
      if (r.kind !== 'UPDATE' && r.kind !== 'CLR') continue;
      const recLSN = dirtyPageTable.get(r.pageId);
      if (recLSN === undefined || recLSN > r.lsn) continue;
      const page = this.storage.getPage(r.pageId);
      if (page.pageLSN >= r.lsn) continue;
      this.storage.setValue(r.pageId, r.key, r.after, r.lsn);
      applied.push(r.lsn);
    }
    return applied;
  }

  /** Roll back every loser transaction, logging CLRs and END records. */
  async undo(records: LogRecord[], txnTable: TxnTable): Promise<UndoResult> {
    const byLsn = new Map<LSN, LogRecord>(records.map((r) => [r.lsn, r]));
    const clrLSNs: LSN[] = [];
    const endedTxns: TxnId[] = [];
    const pending: LSN[] = [];
    for (const entry of txnTable.values()) {
      if (entry.status !== 'committed') pending.push(entry.lastLSN);
    }

    const processed = new Set<LSN>();
    while (pending.length > 0) {
      pending.sort((a, b) => b - a);
      const lsn = pending.shift() as LSN;
      if (processed.has(lsn)) continue;
      processed.add(lsn);
      const record = byLsn.get(lsn);
      if (!record) throw new Error(`undo: dangling LSN ${lsn}`);

      if (record.kind === 'UPDATE') {
        const clr = await this.wal.append({
          kind: 'CLR',
          txnId: record.txnId,
          pageId: record.pageId,
          key: record.key,
          before: record.after,
          after: record.before,
          undoNextLSN: record.prevLSN,
        });
        byLsn.set(clr.lsn, clr);
        clrLSNs.push(clr.lsn);
        this.storage.setValue(record.pageId, record.key, record.before, clr.lsn);
        const entry = txnTable.get(record.txnId);
        if (entry) entry.lastLSN = clr.lsn;
        if (record.prevLSN !== null) pending.push(record.prevLSN);
      } else if (record.kind === 'CLR') {
        // Already compensated before the crash: skip ahead via undoNextLSN.
        if (record.undoNextLSN !== null) {
          pending.push(record.undoNextLSN);
        } else {
          await this.endTxn(record.txnId, txnTable, endedTxns, byLsn);
        }
      } else if (record.kind === 'BEGIN') {
        await this.endTxn(record.txnId, txnTable, endedTxns, byLsn);
      } else if (record.kind === 'ABORT' || record.kind === 'COMMIT') {
        if (record.prevLSN !== null) pending.push(record.prevLSN);
      }
    }

    // Sweep transactions that committed but crashed before writing END.
    for (const entry of [...txnTable.values()]) {
      if (entry.status === 'committed') {
        await this.endTxn(entry.txnId, txnTable, endedTxns, byLsn);
      }
    }
    return { clrLSNs, endedTxns };
  }

  private async endTxn(
    txnId: TxnId,
    txnTable: TxnTable,
    endedTxns: TxnId[],
    byLsn: Map<LSN, LogRecord>,
  ): Promise<void> {
    const end = await this.wal.append({ kind: 'END', txnId });
    byLsn.set(end.lsn, end);
    txnTable.delete(txnId);
    endedTxns.push(txnId);
  }

  /** Full three-phase recovery. Leaves storage flushed and tables drained. */
  async recover(): Promise<RecoveryReport> {
    const records = this.wal.scan();
    const analysis = this.analyze(records);
    const redoApplied = this.redo(records, analysis.dirtyPageTable);
    const undo = await this.undo(records, analysis.txnTable);
    analysis.dirtyPageTable.clear();
    await this.storage.flush();
    return { analysis, redoApplied, undo };
  }
}
