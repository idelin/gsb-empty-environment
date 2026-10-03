/**
 * 事务管理器：产生 UPDATE/COMMIT/ABORT/CHECKPOINT 日志并应用到缓冲池。
 * 遵循 WAL 规则：日志先落盘，数据页可延迟刷盘。
 */
import type {
  LSN,
  PageId,
  TxnId,
  TransactionTableEntry,
  DirtyPageTableEntry,
} from "./logRecord.ts";
import { LogType, TxnStatus } from "./logRecord.ts";
import { WriteAheadLog } from "./wal.ts";
import { BufferPool } from "./storage.ts";

export class TransactionManager {
  private lastLSN = new Map<TxnId, LSN>();
  private activeTxns = new Map<TxnId, TransactionTableEntry>();
  private dirtyPages = new Map<PageId, DirtyPageTableEntry>();
  private readonly wal: WriteAheadLog;
  private readonly bufferPool: BufferPool;

  constructor(wal: WriteAheadLog, bufferPool: BufferPool) {
    this.wal = wal;
    this.bufferPool = bufferPool;
  }

  begin(txnId: TxnId): void {
    if (this.activeTxns.has(txnId)) throw new Error(`事务 ${txnId} 已存在`);
    this.activeTxns.set(txnId, {
      txnId,
      status: TxnStatus.RUNNING,
      lastLSN: 0,
    });
    this.lastLSN.set(txnId, 0);
  }

  /** 更新页：先写 UPDATE 日志，再改缓冲页 */
  update(txnId: TxnId, pageId: PageId, after: string): LSN {
    const page = this.bufferPool.fetch(pageId);
    const rec = this.wal.append({
      prevLSN: this.prevOf(txnId),
      txnId,
      type: LogType.UPDATE,
      pageId,
      before: page.data,
      after,
    });
    this.bufferPool.update(pageId, after, rec.lsn);
    this.track(rec.lsn, txnId, pageId);
    return rec.lsn;
  }

  commit(txnId: TxnId): void {
    const rec = this.wal.append({
      prevLSN: this.prevOf(txnId),
      txnId,
      type: LogType.COMMIT,
    });
    this.track(rec.lsn, txnId);
    const entry = this.activeTxns.get(txnId);
    if (entry) entry.status = TxnStatus.COMMITTED;
    // 简化：提交即写 END 并从事务表移除
    this.wal.append({
      prevLSN: rec.lsn,
      txnId,
      type: LogType.END,
    });
    this.activeTxns.delete(txnId);
  }

  abort(txnId: TxnId): void {
    const rec = this.wal.append({
      prevLSN: this.prevOf(txnId),
      txnId,
      type: LogType.ABORT,
    });
    this.track(rec.lsn, txnId);
    const entry = this.activeTxns.get(txnId);
    if (entry) entry.status = TxnStatus.ABORTING;
  }

  /** 模糊检查点：记录当前事务表与脏页表快照 */
  checkpoint(): LSN {
    const rec = this.wal.append({
      prevLSN: null,
      txnId: 0,
      type: LogType.CHECKPOINT,
      checkpoint: {
        transactionTable: [...this.activeTxns.values()].map((e) => ({
          ...e,
        })),
        dirtyPageTable: [...this.dirtyPages.values()].map((e) => ({ ...e })),
      },
    });
    return rec.lsn;
  }

  private prevOf(txnId: TxnId): LSN | null {
    const lsn = this.lastLSN.get(txnId);
    return lsn && lsn > 0 ? lsn : null;
  }

  private track(lsn: LSN, txnId: TxnId, pageId?: PageId): void {
    this.lastLSN.set(txnId, lsn);
    const entry = this.activeTxns.get(txnId);
    if (entry) entry.lastLSN = lsn;
    if (pageId !== undefined && !this.dirtyPages.has(pageId)) {
      this.dirtyPages.set(pageId, { pageId, recLSN: lsn });
    }
  }
}
