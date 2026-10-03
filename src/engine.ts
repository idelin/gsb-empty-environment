import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { LogManager } from './wal.ts';
import { BufferPool, DiskStorage } from './storage.ts';
import {
  NULL_LSN,
  type CheckpointRecord,
  type CLRRecord,
  type LogRecord,
  type TxTableEntry,
  type UpdateRecord,
} from './log-record.ts';

export interface RecoveryReport {
  analysis: {
    transactionTable: Map<number, TxTableEntry>;
    dirtyPageTable: Map<number, number>;
    redoStartLSN: number;
  };
  redo: { redoneCount: number; skippedCount: number };
  undo: { loserTxIds: number[]; clrCount: number };
}

/**
 * Database engine with ARIES crash recovery: steal/no-force buffer management,
 * physiological logging, fuzzy checkpoints, and the three-phase
 * Analysis / Redo / Undo restart recovery with CLRs.
 */
export class Database {
  wal: LogManager;
  readonly storage: DiskStorage;
  pool: BufferPool;

  private txTable = new Map<number, TxTableEntry>();
  private dirtyPageTable = new Map<number, number>();
  private nextTxId = 1;
  readonly dir: string;

  constructor(dir: string) {
    this.dir = dir;
    mkdirSync(dir, { recursive: true });
    this.wal = new LogManager(join(dir, 'wal.log'));
    this.storage = new DiskStorage(join(dir, 'data.json'));
    this.pool = new BufferPool(this.storage);
  }

  get transactionTable(): ReadonlyMap<number, TxTableEntry> {
    return this.txTable;
  }

  get dpt(): ReadonlyMap<number, number> {
    return this.dirtyPageTable;
  }

  // ---------- normal processing ----------

  begin(): number {
    const txId = this.nextTxId++;
    const rec = this.wal.append({ type: 'begin', txId, prevLSN: NULL_LSN });
    this.txTable.set(txId, { txId, status: 'running', lastLSN: rec.lsn, firstLSN: rec.lsn });
    return txId;
  }

  /** Oldest begin LSN among active transactions; safe lower bound for WAL pruning. */
  oldestActiveTxnBeginLSN(): number | null {
    let oldest: number | null = null;
    for (const entry of this.txTable.values()) {
      if (oldest === null || entry.firstLSN < oldest) oldest = entry.firstLSN;
    }
    return oldest;
  }

  write(txId: number, pageId: number, value: number): void {
    const tx = this.mustKnow(txId);
    const before = this.pool.readPage(pageId).value;
    const rec = this.wal.append({
      type: 'update', txId, prevLSN: tx.lastLSN, pageId, before, after: value,
    }) as UpdateRecord;
    tx.lastLSN = rec.lsn;
    this.pool.writePage(pageId, value, rec.lsn);
    if (!this.dirtyPageTable.has(pageId)) this.dirtyPageTable.set(pageId, rec.lsn);
  }

  commit(txId: number): void {
    const tx = this.mustKnow(txId);
    const rec = this.wal.append({ type: 'commit', txId, prevLSN: tx.lastLSN });
    tx.status = 'committed';
    tx.lastLSN = rec.lsn;
    // Commit record (and everything before it) must be durable before commit returns.
    this.wal.flush(rec.lsn);
    this.end(txId);
  }

  abort(txId: number): void {
    const tx = this.mustKnow(txId);
    const rec = this.wal.append({ type: 'abort', txId, prevLSN: tx.lastLSN });
    tx.status = 'aborted';
    tx.lastLSN = rec.lsn;
    this.undoTransaction(txId);
    this.end(txId);
    this.wal.flush();
  }

  /** Fuzzy checkpoint: snapshot TT/DPT into the log without quiescing. */
  checkpoint(): CheckpointRecord {
    const rec = this.wal.append({
      type: 'checkpoint',
      dpt: [...this.dirtyPageTable.entries()],
      tt: [...this.txTable.entries()].map(([id, e]) => [id, { ...e }] as [number, TxTableEntry]),
    }) as CheckpointRecord;
    this.wal.flush(rec.lsn);
    return rec;
  }

  /** Simulate a crash: unflushed log tail and unflushed dirty pages are lost. */
  crash(): void {
    this.wal.discardBuffer();
    const fresh = new Database(this.dir);
    this.wal = fresh.wal;
    this.pool = fresh.pool;
    this.txTable = new Map();
    this.dirtyPageTable = new Map();
  }

  // ---------- ARIES restart recovery ----------

  recover(): RecoveryReport {
    const log = this.wal.scan();
    const byLsn = new Map<number, LogRecord>(log.map((r) => [r.lsn, r]));

    // ---- Analysis: rebuild TT/DPT starting from the most recent checkpoint. ----
    let startIndex = 0;
    const tt = new Map<number, TxTableEntry>();
    const dpt = new Map<number, number>();
    for (let i = log.length - 1; i >= 0; i--) {
      const rec = log[i];
      if (rec.type === 'checkpoint') {
        for (const [pageId, recLSN] of rec.dpt) dpt.set(pageId, recLSN);
        for (const [txId, entry] of rec.tt) tt.set(txId, { ...entry });
        startIndex = i + 1;
        break;
      }
    }
    for (let i = startIndex; i < log.length; i++) {
      const rec = log[i];
      if (rec.type === 'checkpoint') continue;
      if (rec.type === 'end') {
        tt.delete(rec.txId);
        continue;
      }
      if (rec.type === 'begin') {
        tt.set(rec.txId, { txId: rec.txId, status: 'running', lastLSN: rec.lsn, firstLSN: rec.lsn });
        continue;
      }
      const entry = tt.get(rec.txId) ?? { txId: rec.txId, status: 'running' as const, lastLSN: rec.lsn, firstLSN: rec.lsn };
      entry.lastLSN = rec.lsn;
      if (rec.type === 'commit') entry.status = 'committed';
      if (rec.type === 'abort') entry.status = 'aborted';
      tt.set(rec.txId, entry);
      if ((rec.type === 'update' || rec.type === 'clr') && !dpt.has(rec.pageId)) {
        dpt.set(rec.pageId, rec.lsn);
      }
    }
    this.txTable = tt;
    this.dirtyPageTable = dpt;

    // ---- Redo: repeat history from the oldest recLSN. ----
    const redoStartLSN = dpt.size === 0 ? Number.MAX_SAFE_INTEGER : Math.min(...dpt.values());
    let redoneCount = 0;
    let skippedCount = 0;
    for (const rec of log) {
      if (rec.lsn < redoStartLSN) continue;
      if (rec.type !== 'update' && rec.type !== 'clr') continue;
      const recLSN = dpt.get(rec.pageId);
      if (recLSN === undefined || rec.lsn < recLSN) { skippedCount++; continue; }
      const page = this.pool.readPage(rec.pageId);
      if (page.pageLSN >= rec.lsn) { skippedCount++; continue; }
      this.pool.applyLoggedWrite(rec.pageId, rec.after, rec.lsn);
      redoneCount++;
    }
    this.pool.flushAll();

    // ---- Undo: reverse-scan losers, logging CLRs. ----
    const losers = [...tt.values()].filter((e) => e.status !== 'committed');
    const clrCount = this.undoLosers(losers, byLsn);
    this.pool.flushAll();
    this.wal.flush();

    return {
      analysis: { transactionTable: tt, dirtyPageTable: dpt, redoStartLSN },
      redo: { redoneCount, skippedCount },
      undo: { loserTxIds: losers.map((e) => e.txId).sort((a, b) => a - b), clrCount },
    };
  }

  // ---------- internals ----------

  private mustKnow(txId: number): TxTableEntry {
    const tx = this.txTable.get(txId);
    if (!tx) throw new Error(`unknown transaction ${txId}`);
    return tx;
  }

  private end(txId: number): void {
    const tx = this.mustKnow(txId);
    this.wal.append({ type: 'end', txId, prevLSN: tx.lastLSN });
    this.txTable.delete(txId);
  }

  /** Undo one transaction's updates following its prevLSN chain, writing CLRs. */
  private undoTransaction(txId: number): number {
    const byLsn = new Map<number, LogRecord>(
      this.wal.scanIncludingBuffer().map((r) => [r.lsn, r]),
    );
    let count = 0;
    let lsn = this.mustKnow(txId).lastLSN;
    while (lsn !== NULL_LSN) {
      const rec = byLsn.get(lsn);
      if (!rec || rec.type === 'checkpoint') break;
      if (rec.txId !== txId) break;
      if (rec.type === 'update') {
        this.appendClr(rec);
        count++;
        lsn = rec.prevLSN;
      } else if (rec.type === 'clr') {
        lsn = rec.undoNextLSN;
      } else if (rec.type === 'begin') {
        break;
      } else {
        lsn = rec.prevLSN;
      }
    }
    return count;
  }

  /** ARIES undo pass: max-LSN-first across all loser transactions. */
  private undoLosers(losers: TxTableEntry[], byLsn: Map<number, LogRecord>): number {
    let clrCount = 0;
    const todo = losers.map((e) => e.lastLSN).filter((l) => l !== NULL_LSN);
    while (todo.length > 0) {
      todo.sort((a, b) => b - a);
      const lsn = todo.shift() as number;
      const rec = byLsn.get(lsn);
      if (!rec || rec.type === 'checkpoint') continue;
      if (rec.type === 'update') {
        const clr = this.appendClr(rec);
        byLsn.set(clr.lsn, clr);
        clrCount++;
        if (rec.prevLSN !== NULL_LSN) todo.push(rec.prevLSN);
      } else if (rec.type === 'clr') {
        if (rec.undoNextLSN !== NULL_LSN) todo.push(rec.undoNextLSN);
      } else if (rec.type === 'begin') {
        this.wal.append({ type: 'end', txId: rec.txId, prevLSN: rec.lsn });
        this.txTable.delete(rec.txId);
      } else {
        if (rec.prevLSN !== NULL_LSN) todo.push(rec.prevLSN);
      }
    }
    return clrCount;
  }

  private appendClr(updateRec: UpdateRecord): CLRRecord {
    const tx = this.txTable.get(updateRec.txId);
    const prevLSN = tx ? tx.lastLSN : updateRec.lsn;
    const clr = this.wal.append({
      type: 'clr',
      txId: updateRec.txId,
      prevLSN,
      pageId: updateRec.pageId,
      before: updateRec.after,
      after: updateRec.before,
      undoNextLSN: updateRec.prevLSN,
    }) as CLRRecord;
    if (tx) tx.lastLSN = clr.lsn;
    this.pool.applyLoggedWrite(updateRec.pageId, updateRec.before, clr.lsn);
    if (!this.dirtyPageTable.has(updateRec.pageId)) {
      this.dirtyPageTable.set(updateRec.pageId, clr.lsn);
    }
    return clr;
  }

}
