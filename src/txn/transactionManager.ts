import type { LSN, PageId, TxnId, TxnTableEntry, DirtyPageEntry } from '../log/records.ts';
import { LogManager } from '../log/logManager.ts';
import { BufferPool } from '../storage/bufferPool.ts';

interface ActiveTxn {
  txnId: TxnId;
  status: 'RUNNING' | 'COMMITTING' | 'ABORTING';
  lastLSN: LSN;
}

/**
 * 事务管理器：正常处理路径。
 * 严格 WAL：先 append 日志并 flush 到对应 LSN，再修改缓冲页。
 * 提交：写 COMMIT 并 force 日志，写 END 后从事务表移除。
 */
export class TransactionManager {
  private readonly log: LogManager;
  private readonly pool: BufferPool;
  private nextTxnId: TxnId = 1;
  private readonly txns = new Map<TxnId, ActiveTxn>();
  /** 脏页表（正常处理期间维护，供 checkpoint 快照） */
  private readonly dirtyPages = new Map<PageId, LSN>();

  constructor(log: LogManager, pool: BufferPool) {
    this.log = log;
    this.pool = pool;
  }

  begin(): TxnId {
    const txnId = this.nextTxnId++;
    const lsn = this.log.append({ type: 'BEGIN', prevLSN: 0, txnId });
    this.txns.set(txnId, { txnId, status: 'RUNNING', lastLSN: lsn });
    return txnId;
  }

  /** 更新页槽位；遵循 WAL：日志先落盘，页面后修改 */
  update(txnId: TxnId, pageId: PageId, offset: number, value: string): LSN {
    const txn = this.mustGet(txnId);
    const page = this.pool.fetchPage(pageId);
    const beforeImage = page.data[offset];
    const lsn = this.log.append({
      type: 'UPDATE',
      prevLSN: txn.lastLSN,
      txnId,
      pageId,
      offset,
      beforeImage,
      afterImage: value,
    });
    txn.lastLSN = lsn;
    // WAL：保证日志先于脏页持久化
    this.log.flush(lsn);
    this.pool.applyUpdate(pageId, offset, value, lsn);
    if (!this.dirtyPages.has(pageId)) this.dirtyPages.set(pageId, lsn);
    return lsn;
  }

  commit(txnId: TxnId): void {
    const txn = this.mustGet(txnId);
    txn.status = 'COMMITTING';
    const commitLSN = this.log.append({ type: 'COMMIT', prevLSN: txn.lastLSN, txnId });
    txn.lastLSN = commitLSN;
    // force-at-commit：提交记录落盘即持久，脏页无需落盘（no-force）
    this.log.flush(commitLSN);
    const endLSN = this.log.append({ type: 'END', prevLSN: commitLSN, txnId });
    this.log.flush(endLSN);
    this.txns.delete(txnId);
  }

  /** 模糊检查点：BEGIN_CHECKPOINT + 快照事务表/脏页表 + END_CHECKPOINT */
  checkpoint(): void {
    this.log.append({ type: 'BEGIN_CHECKPOINT', prevLSN: 0, txnId: 0 });
    const txnTable: TxnTableEntry[] = [...this.txns.values()].map((t) => ({
      txnId: t.txnId,
      status: t.status,
      lastLSN: t.lastLSN,
    }));
    const dirtyPageTable: DirtyPageEntry[] = [...this.dirtyPages.entries()].map(
      ([pageId, recLSN]) => ({ pageId, recLSN }),
    );
    const lsn = this.log.append({
      type: 'END_CHECKPOINT',
      prevLSN: 0,
      txnId: 0,
      txnTable,
      dirtyPageTable,
    });
    this.log.flush(lsn);
  }

  /** 崩溃模拟：丢弃内存态（事务表/脏页表/缓冲池由调用方一并丢弃），日志按已刷盘部分留存 */
  activeTxnIds(): TxnId[] {
    return [...this.txns.keys()];
  }

  private mustGet(txnId: TxnId): ActiveTxn {
    const txn = this.txns.get(txnId);
    if (!txn) throw new Error(`txn ${txnId} not active`);
    if (txn.status !== 'RUNNING') throw new Error(`txn ${txnId} is ${txn.status}`);
    return txn;
  }
}
