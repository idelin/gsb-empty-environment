/**
 * 事务管理器：先写日志（WAL），维护 prevLSN 链，支持 commit / abort / checkpoint。
 */

import { WriteAheadLog, type LSN, type TxnStatus, type TxnTableEntry } from './log.ts';
import { BufferPool } from './storage.ts';

export class TransactionManager {
  private readonly lastLSN = new Map<number, LSN>();
  private readonly status = new Map<number, TxnStatus>();
  private nextTxnId = 1;

  private readonly wal: WriteAheadLog;
  private readonly pool: BufferPool;

  constructor(wal: WriteAheadLog, pool: BufferPool) {
    this.wal = wal;
    this.pool = pool;
  }

  begin(): number {
    const txnId = this.nextTxnId++;
    this.lastLSN.set(txnId, 0);
    this.status.set(txnId, 'RUNNING');
    return txnId;
  }

  private assertRunning(txnId: number): void {
    if (this.status.get(txnId) !== 'RUNNING') {
      throw new Error(`transaction ${txnId} is not running`);
    }
  }

  update(txnId: number, pageId: number, offset: number, value: number): LSN {
    this.assertRunning(txnId);
    const page = this.pool.fetchPage(pageId);
    const before = page.read(offset);
    const record = this.wal.append({
      type: 'UPDATE', lsn: 0, txnId,
      prevLSN: this.lastLSN.get(txnId)!,
      pageId, offset, before, after: value,
    });
    page.write(offset, value);
    page.pageLSN = record.lsn;
    this.pool.markDirty(pageId, record.lsn);
    this.lastLSN.set(txnId, record.lsn);
    return record.lsn;
  }

  commit(txnId: number): void {
    this.assertRunning(txnId);
    const commit = this.wal.append({
      type: 'COMMIT', lsn: 0, txnId, prevLSN: this.lastLSN.get(txnId)!,
    });
    this.wal.append({ type: 'END', lsn: 0, txnId, prevLSN: commit.lsn });
    this.lastLSN.delete(txnId);
    this.status.delete(txnId);
  }

  /** 运行时回滚：沿 prevLSN 链写 CLR，最后写 END。 */
  abort(txnId: number): void {
    this.assertRunning(txnId);
    const abort = this.wal.append({
      type: 'ABORT', lsn: 0, txnId, prevLSN: this.lastLSN.get(txnId)!,
    });
    this.status.set(txnId, 'ABORTING');
    let cursor = abort.lsn;
    let lsn = this.lastLSN.get(txnId)!;
    while (lsn !== 0) {
      const record = this.wal.record(lsn);
      if (record.type === 'UPDATE') {
        const page = this.pool.fetchPage(record.pageId);
        const clr = this.wal.append({
          type: 'CLR', lsn: 0, txnId, prevLSN: cursor,
          pageId: record.pageId, offset: record.offset,
          after: record.before, undoNextLSN: record.prevLSN,
        });
        page.write(record.offset, record.before);
        page.pageLSN = clr.lsn;
        this.pool.markDirty(record.pageId, clr.lsn);
        cursor = clr.lsn;
        lsn = record.prevLSN;
      } else if (record.type === 'CLR') {
        lsn = record.undoNextLSN;
      } else {
        lsn = record.prevLSN;
      }
    }
    this.wal.append({ type: 'END', lsn: 0, txnId, prevLSN: cursor });
    this.lastLSN.delete(txnId);
    this.status.delete(txnId);
  }

  /** 检查点：先快照 TT/DPT，再落盘全部脏页，最后写 CHECKPOINT 记录。 */
  checkpoint(): void {
    const txnTable: TxnTableEntry[] = [...this.lastLSN.entries()].map(([txnId, lastLSN]) => ({
      txnId,
      status: this.status.get(txnId)!,
      lastLSN,
    }));
    const dirtyPageTable: Array<[number, LSN]> = [...this.pool.dirtyPageTable.entries()];
    this.pool.flushAll();
    this.wal.append({ type: 'CHECKPOINT', lsn: 0, txnTable, dirtyPageTable });
  }

  txnTableSnapshot(): TxnTableEntry[] {
    return [...this.lastLSN.entries()].map(([txnId, lastLSN]) => ({
      txnId,
      status: this.status.get(txnId)!,
      lastLSN,
    }));
  }
}
