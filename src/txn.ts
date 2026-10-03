import type { LSN, PageId, TTEntry, TxnId } from './types.ts';
import type { LogManager } from './wal.ts';
import type { BufferPool } from './storage.ts';
import type { LockManager } from './lock.ts';

// Transaction manager. Writes WAL before touching pages (write-ahead logging),
// flushes log up to the COMMIT record at commit time.
export class TransactionManager {
  private log: LogManager;
  private bp: BufferPool;
  private locks: LockManager | null;
  private nextTxnId = 1;
  readonly tt = new Map<TxnId, TTEntry>();

  constructor(log: LogManager, bp: BufferPool, locks: LockManager | null = null) {
    this.log = log;
    this.bp = bp;
    this.locks = locks;
  }

  begin(): TxnId {
    const txnId = this.nextTxnId++;
    this.tt.set(txnId, { txnId, status: 'RUNNING', lastLSN: null });
    return txnId;
  }

  update(txnId: TxnId, pageId: PageId, key: string, value: number): LSN {
    const entry = this.mustGet(txnId);
    if (entry.status !== 'RUNNING') throw new Error(`txn ${txnId} is not RUNNING`);
    this.locks?.acquire(txnId, pageId, key);
    const page = this.bp.fetch(pageId);
    const before = page.data[key] ?? 0;
    const record = this.log.append({
      lsn: -1,
      type: 'UPDATE',
      txnId,
      prevLSN: entry.lastLSN,
      pageId,
      key,
      before,
      after: value,
    });
    this.bp.update(pageId, key, value, record.lsn);
    entry.lastLSN = record.lsn;
    return record.lsn;
  }

  commit(txnId: TxnId): void {
    const entry = this.mustGet(txnId);
    const commit = this.log.append({ lsn: -1, type: 'COMMIT', txnId, prevLSN: entry.lastLSN });
    this.log.flush(commit.lsn); // WAL protocol: commit record durable before ack
    entry.status = 'COMMITTED';
    entry.lastLSN = commit.lsn;
    this.log.append({ lsn: -1, type: 'END', txnId, prevLSN: commit.lsn });
    this.tt.delete(txnId);
    this.locks?.releaseAll(txnId);
  }

  abort(txnId: TxnId): void {
    const entry = this.mustGet(txnId);
    entry.status = 'ABORTING';
    const abort = this.log.append({ lsn: -1, type: 'ABORT', txnId, prevLSN: entry.lastLSN });
    entry.lastLSN = abort.lsn;
    this.undoTxn(txnId, entry);
    this.log.append({ lsn: -1, type: 'END', txnId, prevLSN: entry.lastLSN });
    this.tt.delete(txnId);
    this.locks?.releaseAll(txnId);
  }

  // Fuzzy checkpoint: snapshot TT + DPT between BEGIN/END checkpoint records.
  checkpoint(): void {
    const begin = this.log.append({ lsn: -1, type: 'BEGIN_CHECKPOINT' });
    this.log.append({
      lsn: -1,
      type: 'END_CHECKPOINT',
      beginLSN: begin.lsn,
      transactionTable: [...this.tt.values()].map((e) => ({ ...e })),
      dirtyPageTable: this.bp.dirtyPageTable(),
    });
    this.log.flush();
  }

  private undoTxn(txnId: TxnId, entry: TTEntry): void {
    let current: LSN | null = entry.lastLSN;
    while (current !== null) {
      const record = this.log.read(current);
      if (record.type === 'UPDATE') {
        const clr = this.log.append({
          lsn: -1,
          type: 'CLR',
          txnId,
          prevLSN: entry.lastLSN,
          pageId: record.pageId,
          key: record.key,
          before: record.after,
          after: record.before,
          undoNextLSN: record.prevLSN,
        });
        this.bp.update(record.pageId, record.key, record.before, clr.lsn);
        entry.lastLSN = clr.lsn;
        current = record.prevLSN;
      } else if (record.type === 'CLR') {
        current = record.undoNextLSN;
      } else {
        current = record.prevLSN;
      }
    }
  }

  private mustGet(txnId: TxnId): TTEntry {
    const entry = this.tt.get(txnId);
    if (!entry) throw new Error(`unknown txn ${txnId}`);
    return entry;
  }
}
