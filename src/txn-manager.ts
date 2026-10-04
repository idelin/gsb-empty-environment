import type { BufferPool } from './storage.ts';
import type { Wal } from './wal.ts';
import type {
  ClrRecord,
  LSN,
  PageId,
  TxnId,
  TxnTableEntry,
  UpdateRecord,
} from './types.ts';
import { NULL_LSN } from './types.ts';

/**
 * 前向处理期的事务管理器：负责产生 UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT 日志。
 * 每个事务的操作通过独立 promise 链串行化，保证并发提交下
 * 单事务的 prevLSN 链仍然严格连续（并发安全性）。
 */
export class TransactionManager {
  private readonly table = new Map<TxnId, TxnTableEntry>();
  private readonly chains = new Map<TxnId, Promise<unknown>>();

  private readonly wal: Wal;
  private readonly pool: BufferPool;

  constructor(wal: Wal, pool: BufferPool) {
    this.wal = wal;
    this.pool = pool;
  }

  private enqueue<T>(txnId: TxnId, op: () => Promise<T>): Promise<T> {
    const prev = this.chains.get(txnId) ?? Promise.resolve();
    const next = prev.then(op);
    this.chains.set(txnId, next.catch(() => undefined));
    return next;
  }

  private entry(txnId: TxnId): TxnTableEntry {
    let e = this.table.get(txnId);
    if (e === undefined) {
      e = { txnId, status: 'running', lastLSN: NULL_LSN };
      this.table.set(txnId, e);
    }
    return e;
  }

  update(txnId: TxnId, pageId: PageId, value: number): Promise<UpdateRecord> {
    return this.enqueue(txnId, async () => {
      const e = this.entry(txnId);
      if (e.status !== 'running') throw new Error(`事务 ${txnId} 状态为 ${e.status}，禁止更新`);
      const page = this.pool.get(pageId);
      const record = await this.wal.append((lsn) => ({
        type: 'UPDATE',
        lsn,
        txnId,
        prevLSN: e.lastLSN,
        pageId,
        before: page.data,
        after: value,
      }));
      this.pool.applyUpdate(pageId, value, record.lsn);
      e.lastLSN = record.lsn;
      return record;
    });
  }

  commit(txnId: TxnId): Promise<void> {
    return this.enqueue(txnId, async () => {
      const e = this.entry(txnId);
      const commit = await this.wal.append((lsn) => ({
        type: 'COMMIT',
        lsn,
        txnId,
        prevLSN: e.lastLSN,
      }));
      e.status = 'committed';
      e.lastLSN = commit.lsn;
      await this.wal.append((lsn) => ({ type: 'END', lsn, txnId, prevLSN: e.lastLSN }));
      this.table.delete(txnId);
    });
  }

  /** 前向处理期中止：立即反向扫描写 CLR 补偿，最后写 END */
  abort(txnId: TxnId): Promise<void> {
    return this.enqueue(txnId, async () => {
      const e = this.entry(txnId);
      e.status = 'aborting';
      const abort = await this.wal.append((lsn) => ({
        type: 'ABORT',
        lsn,
        txnId,
        prevLSN: e.lastLSN,
      }));
      e.lastLSN = abort.lsn;
      await this.rollback(txnId, e);
      await this.wal.append((lsn) => ({ type: 'END', lsn, txnId, prevLSN: e.lastLSN }));
      this.table.delete(txnId);
    });
  }

  private async rollback(txnId: TxnId, e: TxnTableEntry): Promise<void> {
    const byLSN = new Map<LSN, (typeof this.wal.records)[number]>(
      this.wal.records.map((r) => [r.lsn, r]),
    );
    let lsn = e.lastLSN;
    while (lsn !== NULL_LSN) {
      const record = byLSN.get(lsn);
      if (record === undefined) throw new Error(`回滚链断裂: LSN ${lsn} 不存在`);
      if (record.type === 'UPDATE') {
        const clr = (await this.wal.append((next) => ({
          type: 'CLR',
          lsn: next,
          txnId,
          prevLSN: e.lastLSN,
          pageId: record.pageId,
          before: record.after,
          after: record.before,
          undoNextLSN: record.prevLSN,
        }))) as ClrRecord;
        this.pool.applyUpdate(record.pageId, record.before, clr.lsn);
        e.lastLSN = clr.lsn;
        lsn = record.prevLSN;
      } else if (record.type === 'CLR') {
        lsn = record.undoNextLSN;
      } else {
        lsn = record.prevLSN;
      }
    }
  }

  /** 模糊检查点：BEGIN/END 原子落盘，快照当前事务表与脏页表 */
  async checkpoint(): Promise<void> {
    const transactions = this.transactionTable();
    const dirtyPages = this.pool.dirtyPageTable();
    await this.wal.appendAtomic((first) => [
      { type: 'BEGIN_CHECKPOINT', lsn: first, txnId: null, prevLSN: NULL_LSN },
      {
        type: 'END_CHECKPOINT',
        lsn: first + 1,
        txnId: null,
        prevLSN: NULL_LSN,
        transactions,
        dirtyPages,
      },
    ]);
  }

  transactionTable(): TxnTableEntry[] {
    return [...this.table.values()].map((e) => ({ ...e }));
  }

  /** 等待所有事务的排队操作完成（无泄漏断言用） */
  async quiesce(): Promise<void> {
    await Promise.all([...this.chains.values()]);
    await this.wal.drain();
  }
}
