import { LogType, TxnStatus } from './types.ts';
import type { PageId, TxnId, TxnTableEntry, UpdateRecord } from './types.ts';
import type { WriteAheadLog } from './wal.ts';
import type { BufferPool } from './storage.ts';

/**
 * Transaction manager: emits WAL records and applies updates through the
 * buffer pool. Commit forces the log (not data) — steal/no-force policy.
 */
export class TransactionManager {
  private readonly wal: WriteAheadLog;
  private readonly pool: BufferPool;
  private readonly table = new Map<TxnId, TxnTableEntry>();
  private readonly updatesByTxn = new Map<TxnId, UpdateRecord[]>();
  private nextTxnId = 1;

  constructor(wal: WriteAheadLog, pool: BufferPool) {
    this.wal = wal;
    this.pool = pool;
  }

  begin(): TxnId {
    const txnId = this.nextTxnId++;
    const record = this.wal.append({ type: LogType.BEGIN, txnId, prevLSN: null });
    this.table.set(txnId, { txnId, status: TxnStatus.RUNNING, lastLSN: record.lsn });
    this.updatesByTxn.set(txnId, []);
    return txnId;
  }

  update(txnId: TxnId, pageId: PageId, after: string): number {
    const entry = this.requireEntry(txnId);
    const before = this.pool.get(pageId).data;
    const record = this.wal.append({
      type: LogType.UPDATE,
      txnId,
      prevLSN: entry.lastLSN,
      pageId,
      before,
      after,
    }) as UpdateRecord;
    this.pool.updatePage(pageId, after, record.lsn);
    entry.lastLSN = record.lsn;
    this.updatesByTxn.get(txnId)!.push(record);
    return record.lsn;
  }

  commit(txnId: TxnId): void {
    const entry = this.requireEntry(txnId);
    const record = this.wal.append({
      type: LogType.COMMIT,
      txnId,
      prevLSN: entry.lastLSN,
    });
    entry.status = TxnStatus.COMMITTING;
    entry.lastLSN = record.lsn;
    this.wal.flush(); // force the log up to the commit record
    this.end(txnId);
  }

  abort(txnId: TxnId): void {
    const entry = this.requireEntry(txnId);
    const record = this.wal.append({
      type: LogType.ABORT,
      txnId,
      prevLSN: entry.lastLSN,
    });
    entry.status = TxnStatus.ABORTING;
    entry.lastLSN = record.lsn;
    const updates = this.updatesByTxn.get(txnId) ?? [];
    for (let i = updates.length - 1; i >= 0; i--) {
      const u = updates[i];
      const clr = this.wal.append({
        type: LogType.CLR,
        txnId,
        prevLSN: entry.lastLSN,
        pageId: u.pageId,
        before: u.after,
        after: u.before,
        undoNextLSN: u.prevLSN,
      });
      this.pool.updatePage(u.pageId, u.before, clr.lsn);
      entry.lastLSN = clr.lsn;
    }
    this.end(txnId);
  }

  private end(txnId: TxnId): void {
    const entry = this.requireEntry(txnId);
    this.wal.append({ type: LogType.END, txnId, prevLSN: entry.lastLSN });
    this.wal.flush();
    this.table.delete(txnId);
    this.updatesByTxn.delete(txnId);
  }

  private requireEntry(txnId: TxnId): TxnTableEntry {
    const entry = this.table.get(txnId);
    if (!entry) throw new Error(`transaction ${txnId} is not active`);
    return entry;
  }

  txnTableSnapshot(): TxnTableEntry[] {
    return [...this.table.values()].map((e) => ({ ...e }));
  }

  get activeCount(): number {
    return this.table.size;
  }
}
