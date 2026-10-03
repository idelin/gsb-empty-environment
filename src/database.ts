import { promises as fs } from 'node:fs';
import { join } from 'node:path';
import { WAL } from './wal.ts';
import type { TxStatus } from './wal.ts';
import { PageStore } from './storage.ts';
import { AsyncMutex } from './mutex.ts';
import { analyze, redo, undo } from './recovery.ts';
import type { AnalysisResult, TxnEntry } from './recovery.ts';

export interface RecoveryReport {
  analysis: AnalysisResult;
  redone: number[];
  clrLSNs: number[];
  ended: number[];
}

export interface DatabaseStats {
  txTableSize: number;
  dptSize: number;
  walBuffered: number;
  dirtyPages: number;
  accountedBytes: number;
}

export class Database {
  private static readonly WAL_FLUSH_THRESHOLD = 32;

  private txTable = new Map<number, TxnEntry>();
  private dpt = new Map<number, number>();
  private nextTxId = 1;
  private readonly lock = new AsyncMutex();
  readonly dir: string;
  readonly wal: WAL;
  readonly store: PageStore;

  private constructor(dir: string, wal: WAL, store: PageStore) {
    this.dir = dir;
    this.wal = wal;
    this.store = store;
  }

  static async open(dir: string): Promise<Database> {
    await fs.mkdir(dir, { recursive: true });
    const wal = await WAL.open(join(dir, 'wal.log'));
    const store = await PageStore.open(join(dir, 'pages.json'));
    return new Database(dir, wal, store);
  }

  begin(): Promise<number> {
    return this.lock.runExclusive(() => {
      const txId = this.nextTxId++;
      const record = this.wal.append({ kind: 'begin', txId, prevLSN: 0 });
      this.txTable.set(txId, { txId, status: 'running', lastLSN: record.lsn });
      return txId;
    });
  }

  set(txId: number, pageId: number, slot: number, value: number): Promise<void> {
    return this.lock.runExclusive(async () => {
      const tx = this.mustTx(txId);
      const before = this.store.getSlot(pageId, slot);
      const record = this.wal.append({
        kind: 'update',
        txId,
        prevLSN: tx.lastLSN,
        pageId,
        slot,
        before,
        after: value,
      });
      this.store.setSlot(pageId, slot, value, record.lsn);
      if (!this.dpt.has(pageId)) this.dpt.set(pageId, record.lsn);
      tx.lastLSN = record.lsn;
      if (this.wal.bufferedCount >= Database.WAL_FLUSH_THRESHOLD) await this.wal.flush();
    });
  }

  commit(txId: number): Promise<void> {
    return this.lock.runExclusive(async () => {
      const tx = this.mustTx(txId);
      const commit = this.wal.append({ kind: 'commit', txId, prevLSN: tx.lastLSN });
      await this.wal.flush();
      this.wal.append({ kind: 'end', txId, prevLSN: commit.lsn });
      this.txTable.delete(txId);
    });
  }

  checkpoint(): Promise<void> {
    return this.lock.runExclusive(async () => {
      const begin = this.wal.append({ kind: 'begin_checkpoint', txId: 0, prevLSN: 0 });
      await this.store.flushAll();
      const tt: Array<[number, TxStatus, number]> = [...this.txTable.values()].map(
        (e) => [e.txId, e.status, e.lastLSN],
      );
      const dpt: Array<[number, number]> = [...this.dpt.entries()];
      this.wal.append({
        kind: 'end_checkpoint',
        txId: 0,
        prevLSN: 0,
        beginLSN: begin.lsn,
        tt,
        dpt,
      });
      this.dpt.clear();
      await this.wal.flush();
    });
  }

  crash(): void {
    this.wal.discardBuffer();
    this.store.crash();
    this.txTable.clear();
    this.dpt.clear();
  }

  async recover(): Promise<RecoveryReport> {
    const log = await WAL.readAll(this.wal.path);
    const analysis = analyze(log);
    const redone = redo(log, analysis.dpt, this.store);
    const { clrLSNs, ended } = undo(log, analysis.txTable, this.wal, this.store);
    await this.store.flushAll();
    await this.wal.flush();
    return { analysis, redone, clrLSNs, ended };
  }

  readSlot(pageId: number, slot: number): number {
    return this.store.getSlot(pageId, slot);
  }

  pageLSN(pageId: number): number {
    return this.store.pageLSN(pageId);
  }

  get stats(): DatabaseStats {
    return {
      txTableSize: this.txTable.size,
      dptSize: this.dpt.size,
      walBuffered: this.wal.bufferedCount,
      dirtyPages: this.store.dirtyCount,
      accountedBytes: this.store.accountant.bytes,
    };
  }

  async shutdown(): Promise<void> {
    await this.store.flushAll();
    await this.wal.flush();
    this.store.evictAll();
  }

  private mustTx(txId: number): TxnEntry {
    const tx = this.txTable.get(txId);
    if (!tx) throw new Error(`unknown or finished transaction ${txId}`);
    return tx;
  }
}
