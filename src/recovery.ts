import { LogManager } from './log.ts';
import { BufferPool, Disk } from './storage.ts';
import type { LSN, PageId, TxnId, TxnTableEntry } from './types.ts';
import { NULL_LSN, RecordType } from './types.ts';

export interface AnalysisResult {
  txnTable: Map<TxnId, TxnTableEntry>;
  dpt: Map<PageId, LSN>;
  redoLSN: LSN;
  checkpointBeginLSN: LSN;
}

export interface RedoStats {
  redone: number;
  skipped: number;
}

export interface UndoStats {
  clrCount: number;
  endedTxns: TxnId[];
}

export interface RecoveryResult {
  analysis: AnalysisResult;
  redo: RedoStats;
  undo: UndoStats;
}

export class CrashInjected extends Error {}

class MaxHeap {
  private items: LSN[] = [];
  get size(): number {
    return this.items.length;
  }
  push(v: LSN): void {
    this.items.push(v);
    let i = this.items.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (this.items[p] >= this.items[i]) break;
      [this.items[p], this.items[i]] = [this.items[i], this.items[p]];
      i = p;
    }
  }
  pop(): LSN {
    const top = this.items[0];
    const last = this.items.pop()!;
    if (this.items.length > 0) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        if (l < this.items.length && this.items[l] > this.items[m]) m = l;
        if (r < this.items.length && this.items[r] > this.items[m]) m = r;
        if (m === i) break;
        [this.items[m], this.items[i]] = [this.items[i], this.items[m]];
        i = m;
      }
    }
    return top;
  }
}

export class RecoveryManager {
  private log: LogManager;
  private disk: Disk;
  private buffer: BufferPool;

  constructor(log: LogManager, disk: Disk, buffer: BufferPool) {
    this.log = log;
    this.disk = disk;
    this.buffer = buffer;
  }

  analyze(): AnalysisResult {
    const records = this.log.stableRecords();
    const txnTable = new Map<TxnId, TxnTableEntry>();
    const dpt = new Map<PageId, LSN>();
    let startIdx = 0;
    let checkpointBeginLSN = NULL_LSN;

    for (let i = records.length - 1; i >= 0; i--) {
      const r = records[i];
      if (r.type === RecordType.END_CHECKPOINT) {
        checkpointBeginLSN = r.beginLSN ?? NULL_LSN;
        for (const e of r.txnTable ?? []) txnTable.set(e.txnId, { ...e });
        for (const [p, lsn] of r.dpt ?? []) dpt.set(p, lsn);
        const idx = records.findIndex((x) => x.lsn === checkpointBeginLSN);
        startIdx = idx >= 0 ? idx : i;
        break;
      }
    }

    for (let i = startIdx; i < records.length; i++) {
      const r = records[i];
      switch (r.type) {
        case RecordType.UPDATE: {
          const e = txnTable.get(r.txnId) ?? { txnId: r.txnId, status: 'RUNNING' as const, lastLSN: NULL_LSN };
          e.status = 'RUNNING';
          e.lastLSN = r.lsn;
          txnTable.set(r.txnId, e);
          if (r.pageId !== undefined && !dpt.has(r.pageId)) dpt.set(r.pageId, r.lsn);
          break;
        }
        case RecordType.CLR: {
          const e = txnTable.get(r.txnId) ?? { txnId: r.txnId, status: 'RUNNING' as const, lastLSN: NULL_LSN };
          e.lastLSN = r.lsn;
          txnTable.set(r.txnId, e);
          if (r.pageId !== undefined && !dpt.has(r.pageId)) dpt.set(r.pageId, r.lsn);
          break;
        }
        case RecordType.COMMIT: {
          const e = txnTable.get(r.txnId) ?? { txnId: r.txnId, status: 'COMMITTED' as const, lastLSN: NULL_LSN };
          e.status = 'COMMITTED';
          e.lastLSN = r.lsn;
          txnTable.set(r.txnId, e);
          break;
        }
        case RecordType.ABORT: {
          const e = txnTable.get(r.txnId) ?? { txnId: r.txnId, status: 'ABORTING' as const, lastLSN: NULL_LSN };
          e.status = 'ABORTING';
          e.lastLSN = r.lsn;
          txnTable.set(r.txnId, e);
          break;
        }
        case RecordType.END:
          txnTable.delete(r.txnId);
          break;
        default:
          break;
      }
    }

    let redoLSN = NULL_LSN;
    for (const lsn of dpt.values()) {
      if (redoLSN === NULL_LSN || lsn < redoLSN) redoLSN = lsn;
    }
    return { txnTable, dpt, redoLSN, checkpointBeginLSN };
  }

  redo(analysis: AnalysisResult): RedoStats {
    const stats: RedoStats = { redone: 0, skipped: 0 };
    if (analysis.redoLSN === NULL_LSN) return stats;
    for (const r of this.log.stableRecords()) {
      if (r.lsn < analysis.redoLSN) continue;
      if (r.type !== RecordType.UPDATE && r.type !== RecordType.CLR) continue;
      const pageId = r.pageId!;
      const recLSN = analysis.dpt.get(pageId);
      if (recLSN === undefined || r.lsn < recLSN) {
        stats.skipped++;
        continue;
      }
      const page = this.buffer.fix(pageId);
      if (page.pageLSN >= r.lsn) {
        stats.skipped++;
        continue;
      }
      page.value = r.after!;
      page.pageLSN = r.lsn;
      this.buffer.markDirty(page, r.lsn);
      stats.redone++;
    }
    return stats;
  }

  undo(analysis: AnalysisResult, onCLR?: (count: number) => void): UndoStats {
    const heap = new MaxHeap();
    for (const e of analysis.txnTable.values()) {
      if (e.status !== 'COMMITTED' && e.lastLSN !== NULL_LSN) heap.push(e.lastLSN);
    }

    let clrCount = 0;
    const endedTxns: TxnId[] = [];
    const endTxn = (txnId: TxnId, prevLSN: LSN) => {
      this.log.append({ type: RecordType.END, txnId, prevLSN });
      endedTxns.push(txnId);
    };

    while (heap.size > 0) {
      const lsn = heap.pop();
      const rec = this.log.get(lsn);
      if (!rec) throw new Error(`undo: missing log record for LSN ${lsn}`);
      if (rec.type === RecordType.UPDATE) {
        const clr = this.log.append({
          type: RecordType.CLR,
          txnId: rec.txnId,
          prevLSN: rec.lsn,
          pageId: rec.pageId,
          before: rec.after,
          after: rec.before,
          undoNextLSN: rec.prevLSN,
        });
        const page = this.buffer.fix(rec.pageId!);
        page.value = rec.before!;
        page.pageLSN = clr.lsn;
        this.buffer.markDirty(page, clr.lsn);
        clrCount++;
        onCLR?.(clrCount);
        if (rec.prevLSN !== NULL_LSN) heap.push(rec.prevLSN);
        else endTxn(rec.txnId, clr.lsn);
      } else if (rec.type === RecordType.CLR) {
        const next = rec.undoNextLSN ?? NULL_LSN;
        if (next !== NULL_LSN) heap.push(next);
        else endTxn(rec.txnId, rec.lsn);
      } else {
        if (rec.prevLSN !== NULL_LSN) heap.push(rec.prevLSN);
        else endTxn(rec.txnId, rec.lsn);
      }
    }

    for (const e of analysis.txnTable.values()) {
      if (e.status === 'COMMITTED') endTxn(e.txnId, e.lastLSN);
    }
    return { clrCount, endedTxns };
  }

  recover(opts?: { crashAfterCLRs?: number }): RecoveryResult {
    const analysis = this.analyze();
    const redo = this.redo(analysis);
    let clrSeen = 0;
    const undo = this.undo(analysis, () => {
      clrSeen++;
      if (opts?.crashAfterCLRs !== undefined && clrSeen === opts.crashAfterCLRs) {
        this.buffer.crash();
        this.log.crash();
        throw new CrashInjected(`crash injected after ${clrSeen} CLR(s) during undo`);
      }
    });
    this.log.force();
    this.buffer.flushAll();
    return { analysis, redo, undo };
  }
}
