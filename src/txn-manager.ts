/**
 * 事务管理器：ARIES 协议的运行时一侧，负责生成 WAL。
 *
 * 协议状态机：RUNNING --commit--> COMMITTED --end--> (移除)
 *             RUNNING --abort-->  ABORTING  --end--> (移除)
 * 约束：非法状态迁移直接抛错；提交时 COMMIT 记录强制 fsync；
 * 页修改必须先写日志再改缓存页（WAL 先行）。
 */
import {
  LogType,
  TxnStatus,
  type TransactionTable,
  type TxnId,
  type TxnStatusName,
} from './log-record.ts';
import { WalIndex, WalWriter, readWal } from './wal.ts';
import { BufferPool } from './storage.ts';
import { rollbackTransaction } from './recovery.ts';

interface LiveTxn {
  status: TxnStatusName;
  lastLSN: number;
}

export class TransactionManager {
  readonly wal: WalWriter;
  readonly buffer: BufferPool;
  #txns = new Map<TxnId, LiveTxn>();
  #nextTxnId = 1;
  #closed = false;

  constructor(wal: WalWriter, buffer: BufferPool) {
    this.wal = wal;
    this.buffer = buffer;
  }

  get isClosed(): boolean {
    return this.#closed;
  }

  begin(): TxnId {
    this.#assertOpen();
    const txnId = this.#nextTxnId;
    this.#nextTxnId += 1;
    this.#txns.set(txnId, { status: TxnStatus.Running, lastLSN: 0 });
    return txnId;
  }

  update(txnId: TxnId, pageId: string, after: number): void {
    this.#assertOpen();
    const txn = this.#requireStatus(txnId, [TxnStatus.Running], 'update');
    const page = this.buffer.read(pageId);
    // WAL 先行：先持久化日志，再应用页修改。
    const record = this.wal.append({
      type: LogType.Update,
      txnId,
      prevLSN: txn.lastLSN === 0 ? null : txn.lastLSN,
      pageId,
      before: page.value,
      after,
    });
    this.buffer.apply(pageId, after, record.lsn);
    txn.lastLSN = record.lsn;
  }

  commit(txnId: TxnId): void {
    this.#assertOpen();
    const txn = this.#requireStatus(txnId, [TxnStatus.Running], 'commit');
    const commit = this.wal.append(
      { type: LogType.Commit, txnId, prevLSN: txn.lastLSN === 0 ? null : txn.lastLSN },
      { fsync: true }, // 提交点：日志强制落盘后才算持久
    );
    txn.status = TxnStatus.Committed;
    txn.lastLSN = commit.lsn;
    this.wal.append({ type: LogType.End, txnId, prevLSN: txn.lastLSN });
    this.#txns.delete(txnId);
  }

  abort(txnId: TxnId): void {
    this.#assertOpen();
    const txn = this.#requireStatus(txnId, [TxnStatus.Running], 'abort');
    const abort = this.wal.append({
      type: LogType.Abort,
      txnId,
      prevLSN: txn.lastLSN === 0 ? null : txn.lastLSN,
    });
    txn.status = TxnStatus.Aborting;
    // 与恢复期 Undo 共用同一套补偿逻辑（CLR 链）。
    const index = new WalIndex(readWal(this.wal.path));
    // 经缓冲池补偿：不绕过缓存直接写盘，避免覆盖其他事务的未落盘修改。
    const ctx = { index, wal: this.wal, storage: this.buffer };
    let lastLSN = abort.lsn;
    if (abort.prevLSN !== null) {
      const step = rollbackTransaction(ctx, txnId, abort.prevLSN, abort.lsn);
      lastLSN = step.lastLSN;
    }
    this.wal.append({ type: LogType.End, txnId, prevLSN: lastLSN });
    this.#txns.delete(txnId);
  }

  /** 模糊检查点：BEGIN + END（携带事务表与脏页表快照）。 */
  checkpoint(): void {
    this.#assertOpen();
    this.wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null });
    const txnTable: TransactionTable = new Map();
    for (const [txnId, txn] of this.#txns) {
      txnTable.set(txnId, { txnId, status: txn.status, lastLSN: txn.lastLSN });
    }
    this.wal.append({
      type: LogType.EndCheckpoint,
      txnId: null,
      prevLSN: null,
      txnTable: [...txnTable.values()],
      dirtyPageTable: [...this.buffer.dirtyPageTable().entries()],
    });
  }

  activeTransactions(): TxnId[] {
    return [...this.#txns.keys()];
  }

  close(): void {
    if (!this.#closed) {
      if (this.#txns.size > 0) {
        throw new Error(`close with ${this.#txns.size} active transactions (leak)`);
      }
      this.wal.close();
      this.#closed = true;
    }
  }

  #assertOpen(): void {
    if (this.#closed) throw new Error('transaction manager is closed');
  }

  #requireStatus(txnId: TxnId, allowed: TxnStatusName[], op: string): LiveTxn {
    const txn = this.#txns.get(txnId);
    if (!txn) throw new Error(`${op}: unknown transaction ${txnId}`);
    if (!allowed.includes(txn.status)) {
      throw new Error(`${op}: txn ${txnId} in illegal state ${txn.status}`);
    }
    return txn;
  }
}
