/**
 * Normal-processing facade: transactions over the WAL + buffer pool.
 * Implements steal / no-force buffering, so commits never flush data pages
 * and crashes can leave dirty (even uncommitted) pages on disk.
 */

import { Wal } from "./wal";
import { PageStore, BufferPool } from "./storage";
import { RecordType, TxnEntry } from "./logrecord";

export class Database {
  readonly wal: Wal;
  readonly store: PageStore;
  readonly pool: BufferPool;
  private txns = new Map<number, TxnEntry>();
  private dirtyPages = new Map<number, number>(); // pageId -> recLSN
  private nextTxnId = 1;
  private crashed = false;

  constructor(walPath: string, dataPath: string) {
    this.wal = Wal.open(walPath);
    this.store = new PageStore(dataPath);
    this.pool = new BufferPool(this.store);
  }

  get activeTxns(): ReadonlyMap<number, TxnEntry> {
    return this.txns;
  }

  begin(): number {
    const txnId = this.nextTxnId++;
    const lsn = this.wal.appendSync({ type: RecordType.Begin, lsn: 0, txnId, prevLSN: 0 });
    this.txns.set(txnId, { txnId, status: "running", lastLSN: lsn });
    return txnId;
  }

  /** Writes `data` at `offset` of `pageId`; returns the update's LSN. */
  update(txnId: number, pageId: number, offset: number, data: Buffer): number {
    const txn = this.requireRunning(txnId);
    const before = this.pool.readBytes(pageId, offset, data.length);
    const lsn = this.wal.appendSync({
      type: RecordType.Update, lsn: 0, txnId, prevLSN: txn.lastLSN,
      pageId, offset, before, after: data,
    });
    this.pool.applyBytes(pageId, offset, data, lsn);
    txn.lastLSN = lsn;
    if (!this.dirtyPages.has(pageId)) this.dirtyPages.set(pageId, lsn);
    return lsn;
  }

  /** No-force commit: the commit record is durable, data pages are not. */
  commit(txnId: number): void {
    const txn = this.requireRunning(txnId);
    const lsn = this.wal.appendSync({
      type: RecordType.Commit, lsn: 0, txnId, prevLSN: txn.lastLSN,
    });
    txn.status = "committed";
    txn.lastLSN = lsn;
  }

  /** Writes the END record once a committed txn has finished cleanup. */
  endTxn(txnId: number): void {
    const txn = this.txns.get(txnId);
    if (!txn || txn.status !== "committed") {
      throw new Error(`txn ${txnId} is not committed`);
    }
    this.wal.appendSync({ type: RecordType.End, lsn: 0, txnId, prevLSN: txn.lastLSN });
    this.txns.delete(txnId);
  }

  /** Fuzzy checkpoint: snapshots ATT + DPT without quiescing or flushing. */
  checkpoint(): number {
    return this.wal.appendSync({
      type: RecordType.Checkpoint, lsn: 0, txnId: 0, prevLSN: 0,
      txns: [...this.txns.values()].map((t) => ({ ...t })),
      dirtyPages: [...this.dirtyPages.entries()],
    });
  }

  /** Simulates a steal eviction: flushes one dirty page (even uncommitted). */
  stealPage(pageId: number): void {
    this.pool.flushPage(pageId);
    this.dirtyPages.delete(pageId);
  }

  /** Simulates a crash: dirty buffers vanish, WAL and disk pages survive. */
  crash(): void {
    this.pool.discardAll();
    this.wal.close();
    this.store.close();
    this.crashed = true;
  }

  /** Clean shutdown used by leak checks: everything flushed and closed. */
  shutdown(): void {
    this.pool.flushAll();
    this.pool.close();
    this.wal.close();
    this.store.close();
  }

  get isCrashed(): boolean {
    return this.crashed;
  }

  private requireRunning(txnId: number): TxnEntry {
    const txn = this.txns.get(txnId);
    if (!txn || txn.status !== "running") {
      throw new Error(`txn ${txnId} is not running`);
    }
    return txn;
  }
}
