import { BufferPool } from './storage.ts';
import { LockManager } from './lockManager.ts';
import { WriteAheadLog } from './wal.ts';
import { NULL_LSN } from './types.ts';
import type {
  CLRRecord,
  DirtyPageEntry,
  LSN,
  LogRecord,
  PageId,
  TxId,
  TxTableEntry,
} from './types.ts';

export interface TxHooks {
  /** 每条日志追加并落盘后触发；抛出异常可模拟崩溃（日志已持久化） */
  afterAppend?: (rec: LogRecord) => void;
}

/**
 * 事务管理器：前向处理（UPDATE/COMMIT）与反向补偿（ABORT → CLR 链）。
 * 回滚与崩溃恢复的 Undo 共用同一条 CLR 补偿逻辑，保证语义一致。
 */
export class TransactionManager {
  readonly txTable = new Map<TxId, TxTableEntry>();
  readonly dirtyPageTable = new Map<PageId, LSN>();
  private nextTxId: TxId = 1;
  private readonly wal: WriteAheadLog;
  private readonly bufferPool: BufferPool;
  private readonly lockManager: LockManager;
  private readonly hooks: TxHooks;

  constructor(
    wal: WriteAheadLog,
    bufferPool: BufferPool,
    lockManager: LockManager,
    hooks: TxHooks = {},
  ) {
    this.wal = wal;
    this.bufferPool = bufferPool;
    this.lockManager = lockManager;
    this.hooks = hooks;
  }

  private append<T extends Omit<LogRecord, 'lsn'>>(rec: T): T & { lsn: LSN } {
    const full = this.wal.append(rec);
    this.hooks.afterAppend?.(full);
    return full;
  }

  begin(): TxId {
    const txId = this.nextTxId++;
    this.txTable.set(txId, { txId, status: 'RUNNING', lastLSN: NULL_LSN });
    return txId;
  }

  /** 前向更新：先写 UPDATE 日志（同步落盘），再改缓冲池页 */
  async update(txId: TxId, pageId: PageId, after: string): Promise<LSN> {
    const entry = this.mustGet(txId);
    if (entry.status !== 'RUNNING') {
      throw new Error(`tx ${txId} not RUNNING: ${entry.status}`);
    }
    await this.lockManager.acquire(txId, pageId);
    const page = this.bufferPool.fetch(pageId);
    const rec = this.append({
      type: 'UPDATE',
      txId,
      prevLSN: entry.lastLSN,
      pageId,
      before: page.data,
      after,
    });
    entry.lastLSN = rec.lsn;
    if (!this.dirtyPageTable.has(pageId)) {
      this.dirtyPageTable.set(pageId, rec.lsn);
    }
    this.bufferPool.set(pageId, after, rec.lsn);
    return rec.lsn;
  }

  async read(txId: TxId, pageId: PageId): Promise<string> {
    this.mustGet(txId);
    await this.lockManager.acquire(txId, pageId);
    return this.bufferPool.fetch(pageId).data;
  }

  /** 提交：COMMIT 落盘即持久化点，随后写 END 并释放锁 */
  async commit(txId: TxId): Promise<void> {
    const entry = this.mustGet(txId);
    const commit = this.append({ type: 'COMMIT', txId, prevLSN: entry.lastLSN });
    entry.status = 'COMMITTING';
    entry.lastLSN = commit.lsn;
    this.append({ type: 'END', txId, prevLSN: entry.lastLSN });
    this.txTable.delete(txId);
    this.lockManager.releaseAll(txId);
  }

  /** 中止：写 ABORT 后沿日志链反向补偿（CLR），直至链首再写 END */
  async abort(txId: TxId): Promise<void> {
    const entry = this.mustGet(txId);
    if (entry.status === 'COMMITTING') {
      throw new Error(`tx ${txId} already committing`);
    }
    const abort = this.append({ type: 'ABORT', txId, prevLSN: entry.lastLSN });
    entry.status = 'ABORTING';
    entry.lastLSN = abort.lsn;
    this.rollback(txId);
    this.lockManager.releaseAll(txId);
  }

  /**
   * 反向补偿核心：从事务最后一条日志逆向扫描，
   * 对每条 UPDATE 写 CLR（after 置为前镜像），CLR 则沿 undoNextLSN 跳过已补偿部分。
   * 幂等性：每条 UPDATE 至多被一条 CLR 补偿，崩溃重入时经 undoNextLSN 续扫。
   */
  rollback(txId: TxId): CLRRecord[] {
    const entry = this.mustGet(txId);
    const clrs: CLRRecord[] = [];
    let lsn = entry.lastLSN;
    while (lsn !== NULL_LSN) {
      const rec = this.wal.getRecord(lsn);
      if (rec.type === 'UPDATE') {
        const clr = this.append({
          type: 'CLR',
          txId,
          prevLSN: entry.lastLSN,
          pageId: rec.pageId,
          before: rec.after,
          after: rec.before,
          undoNextLSN: rec.prevLSN,
        });
        entry.lastLSN = clr.lsn;
        if (!this.dirtyPageTable.has(rec.pageId)) {
          this.dirtyPageTable.set(rec.pageId, clr.lsn);
        }
        this.bufferPool.set(rec.pageId, rec.before, clr.lsn);
        clrs.push(clr);
        lsn = rec.prevLSN;
      } else if (rec.type === 'CLR') {
        lsn = rec.undoNextLSN;
      } else if (rec.type === 'ABORT' || rec.type === 'COMMIT') {
        lsn = rec.prevLSN;
      } else {
        throw new Error(`unexpected record in undo chain: ${rec.type}`);
      }
    }
    this.append({ type: 'END', txId, prevLSN: entry.lastLSN });
    this.txTable.delete(txId);
    return clrs;
  }

  /** 刷盘后用已持久化的 pageLSN 清理脏页表（无泄漏断言的基础） */
  onPagesFlushed(flushed: Map<PageId, LSN>): void {
    for (const [pageId, pageLSN] of flushed) {
      const recLSN = this.dirtyPageTable.get(pageId);
      if (recLSN !== undefined && recLSN <= pageLSN) {
        this.dirtyPageTable.delete(pageId);
      }
    }
  }

  snapshotTxTable(): TxTableEntry[] {
    return [...this.txTable.values()].map((e) => ({ ...e }));
  }

  snapshotDirtyPageTable(): DirtyPageEntry[] {
    return [...this.dirtyPageTable.entries()].map(([pageId, recLSN]) => ({
      pageId,
      recLSN,
    }));
  }

  private mustGet(txId: TxId): TxTableEntry {
    const entry = this.txTable.get(txId);
    if (!entry) throw new Error(`unknown tx: ${txId}`);
    return entry;
  }
}
