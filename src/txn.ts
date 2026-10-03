import type { BufferPool } from './bufferpool.ts';
import type { LogRecord, LSN, PageId, TxnId, TxnTableEntry } from './types.ts';
import { deepCopy } from './util.ts';
import type { WAL } from './wal.ts';

/**
 * Transaction manager. All log+page critical sections run through a latch
 * (modelling page latching), so concurrent transactions observe consistent
 * before-images and monotonic pageLSNs.
 */
export class TransactionManager {
  private readonly wal: WAL;
  private readonly bp: BufferPool;
  private readonly txns = new Map<TxnId, TxnTableEntry>();
  private nextTxnId: number;
  private latch: Promise<void> = Promise.resolve();

  constructor(wal: WAL, bp: BufferPool) {
    this.wal = wal;
    this.bp = bp;
    this.nextTxnId = wal.maxTxnId() + 1;
  }

  private synchronized<T>(fn: () => Promise<T>): Promise<T> {
    const result = this.latch.then(fn);
    this.latch = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  begin(): Promise<TxnId> {
    return this.synchronized(async () => {
      const txnId = this.nextTxnId++;
      const rec = await this.wal.append({ type: 'BEGIN', txnId, prevLSN: 0 });
      this.txns.set(txnId, { txnId, status: 'RUNNING', lastLSN: rec.lsn });
      return txnId;
    });
  }

  set(txnId: TxnId, pageId: PageId, key: string, value: unknown): Promise<void> {
    return this.synchronized(async () => {
      const txn = this.mustGet(txnId);
      const page = this.bp.fetchPage(pageId);
      const before = key in page.data ? deepCopy(page.data[key]) : null;
      const rec = await this.wal.append({
        type: 'UPDATE',
        txnId,
        prevLSN: txn.lastLSN,
        pageId,
        key,
        before,
        after: deepCopy(value),
      });
      txn.lastLSN = rec.lsn;
      this.bp.applyUpdate(pageId, key, value, rec.lsn);
    });
  }

  /** Forces the commit record to durable storage (group commit). */
  commit(txnId: TxnId): Promise<void> {
    return this.synchronized(async () => {
      const txn = this.mustGet(txnId);
      const rec = await this.wal.append({ type: 'COMMIT', txnId, prevLSN: txn.lastLSN });
      txn.lastLSN = rec.lsn;
      txn.status = 'COMMITTED';
      try {
        await this.wal.flush(rec.lsn);
      } catch (err) {
        // Commit could not be made durable (disk full / power loss):
        // the transaction is still a loser and must be rolled back.
        txn.status = 'RUNNING';
        throw err;
      }
      const end = await this.wal.append({ type: 'END', txnId, prevLSN: txn.lastLSN });
      txn.lastLSN = end.lsn;
      this.txns.delete(txnId);
    });
  }

  /** Logical undo of a single transaction, writing CLRs as it goes. */
  abort(txnId: TxnId): Promise<void> {
    return this.synchronized(async () => {
      const txn = this.mustGet(txnId);
      const abortRec = await this.wal.append({ type: 'ABORT', txnId, prevLSN: txn.lastLSN });
      txn.lastLSN = abortRec.lsn;
      txn.status = 'ABORTING';
      let lsn = abortRec.prevLSN;
      while (lsn > 0) {
        const rec = this.wal.recordAt(lsn);
        if (!rec) throw new Error(`broken undo chain at LSN ${lsn}`);
        if (rec.type === 'UPDATE') {
          const clr = await this.wal.append({
            type: 'CLR',
            txnId,
            prevLSN: txn.lastLSN,
            pageId: rec.pageId,
            key: rec.key,
            before: rec.after,
            after: rec.before,
            undoNextLSN: rec.prevLSN,
          });
          txn.lastLSN = clr.lsn;
          this.applyBeforeImage(rec, clr.lsn);
          lsn = rec.prevLSN;
        } else if (rec.type === 'CLR') {
          lsn = rec.undoNextLSN ?? 0;
        } else if (rec.type === 'BEGIN') {
          lsn = 0;
        } else {
          lsn = rec.prevLSN;
        }
      }
      const end = await this.wal.append({ type: 'END', txnId, prevLSN: txn.lastLSN });
      txn.lastLSN = end.lsn;
      this.txns.delete(txnId);
    });
  }

  /** Fuzzy checkpoint: snapshots the transaction table and DPT into the log. */
  checkpoint(): Promise<LSN> {
    return this.synchronized(async () => {
      const tt: Record<string, TxnTableEntry> = {};
      for (const [id, entry] of this.txns) tt[id] = { ...entry };
      const rec = await this.wal.append({
        type: 'CHECKPOINT',
        txnId: 0,
        prevLSN: 0,
        tt,
        dpt: this.bp.dirtySnapshot(),
      });
      await this.wal.flush(rec.lsn);
      this.wal.writeMaster(rec.lsn);
      return rec.lsn;
    });
  }

  openTxnIds(): TxnId[] {
    return [...this.txns.keys()].sort((a, b) => a - b);
  }

  private applyBeforeImage(rec: LogRecord, lsn: LSN): void {
    if (rec.before === null || rec.before === undefined) {
      this.bp.applyDelete(rec.pageId!, rec.key!, lsn);
    } else {
      this.bp.applyUpdate(rec.pageId!, rec.key!, deepCopy(rec.before), lsn);
    }
  }

  private mustGet(txnId: TxnId): TxnTableEntry {
    const txn = this.txns.get(txnId);
    if (!txn) throw new Error(`unknown or finished transaction ${txnId}`);
    return txn;
  }
}
