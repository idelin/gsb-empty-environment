import { LogManager } from './log.ts';
import { BufferPool } from './storage.ts';
import type { PageId, TxnId, TxnStatus } from './types.ts';
import { NULL_LSN, RecordType } from './types.ts';

export class LatchManager {
  private held = new Map<PageId, TxnId>();

  tryAcquire(pageId: PageId, txnId: TxnId): boolean {
    const owner = this.held.get(pageId);
    if (owner === undefined || owner === txnId) {
      this.held.set(pageId, txnId);
      return true;
    }
    return false;
  }

  acquire(pageId: PageId, txnId: TxnId): void {
    if (!this.tryAcquire(pageId, txnId)) {
      throw new Error(
        `latch conflict on page ${pageId}: held by txn ${this.held.get(pageId)}, requested by txn ${txnId}`,
      );
    }
  }

  release(pageId: PageId, txnId: TxnId): void {
    if (this.held.get(pageId) !== txnId) {
      throw new Error(`latch release violation on page ${pageId} by txn ${txnId}`);
    }
    this.held.delete(pageId);
  }

  get outstanding(): number {
    return this.held.size;
  }

  assertNoLeaks(): void {
    if (this.held.size > 0) {
      throw new Error(`latch leak: ${this.held.size} latch(es) still held`);
    }
  }
}

interface TxnEntry {
  status: TxnStatus;
  lastLSN: number;
}

export class TransactionManager {
  private log: LogManager;
  private buffer: BufferPool;
  txnTable = new Map<TxnId, TxnEntry>();
  private nextTxnId = 1;

  constructor(log: LogManager, buffer: BufferPool) {
    this.log = log;
    this.buffer = buffer;
  }

  begin(): TxnId {
    const id = this.nextTxnId++;
    this.txnTable.set(id, { status: 'RUNNING', lastLSN: NULL_LSN });
    return id;
  }

  update(txnId: TxnId, pageId: PageId, delta: number): void {
    const entry = this.mustGet(txnId);
    const page = this.buffer.fix(pageId);
    const before = page.value;
    const after = before + delta;
    const rec = this.log.append({
      type: RecordType.UPDATE,
      txnId,
      prevLSN: entry.lastLSN,
      pageId,
      before,
      after,
    });
    entry.lastLSN = rec.lsn;
    page.value = after;
    page.pageLSN = rec.lsn;
    this.buffer.markDirty(page, rec.lsn);
  }

  commit(txnId: TxnId): void {
    const entry = this.mustGet(txnId);
    const c = this.log.append({ type: RecordType.COMMIT, txnId, prevLSN: entry.lastLSN });
    entry.lastLSN = c.lsn;
    entry.status = 'COMMITTED';
    this.log.force();
    this.log.append({ type: RecordType.END, txnId, prevLSN: entry.lastLSN });
    this.txnTable.delete(txnId);
  }

  abort(txnId: TxnId): void {
    const entry = this.mustGet(txnId);
    const ab = this.log.append({ type: RecordType.ABORT, txnId, prevLSN: entry.lastLSN });
    entry.lastLSN = ab.lsn;
    entry.status = 'ABORTING';
    let lsn = ab.prevLSN;
    while (lsn !== NULL_LSN) {
      const rec = this.log.get(lsn);
      if (!rec) throw new Error(`abort: missing log record for LSN ${lsn}`);
      if (rec.type === RecordType.UPDATE) {
        const clr = this.log.append({
          type: RecordType.CLR,
          txnId,
          prevLSN: entry.lastLSN,
          pageId: rec.pageId,
          before: rec.after,
          after: rec.before,
          undoNextLSN: rec.prevLSN,
        });
        entry.lastLSN = clr.lsn;
        const page = this.buffer.fix(rec.pageId!);
        page.value = rec.before!;
        page.pageLSN = clr.lsn;
        this.buffer.markDirty(page, clr.lsn);
        lsn = rec.prevLSN;
      } else if (rec.type === RecordType.CLR) {
        lsn = rec.undoNextLSN ?? NULL_LSN;
      } else {
        lsn = rec.prevLSN;
      }
    }
    this.log.append({ type: RecordType.END, txnId, prevLSN: entry.lastLSN });
    this.txnTable.delete(txnId);
  }

  discardCrashedState(): void {
    this.txnTable.clear();
  }

  checkpoint(): void {
    const begin = this.log.append({
      type: RecordType.BEGIN_CHECKPOINT,
      txnId: 0,
      prevLSN: NULL_LSN,
    });
    const txnSnap = [...this.txnTable].map(([txnId, e]) => ({
      txnId,
      status: e.status,
      lastLSN: e.lastLSN,
    }));
    const dptSnap = this.buffer.dirtyPages();
    this.log.append({
      type: RecordType.END_CHECKPOINT,
      txnId: 0,
      prevLSN: NULL_LSN,
      beginLSN: begin.lsn,
      txnTable: txnSnap,
      dpt: dptSnap,
    });
    this.log.force();
    this.buffer.flushAll();
  }

  private mustGet(txnId: TxnId): TxnEntry {
    const e = this.txnTable.get(txnId);
    if (!e) throw new Error(`unknown txn ${txnId}`);
    return e;
  }
}
