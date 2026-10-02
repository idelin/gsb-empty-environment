/**
 * 内存快照隔离 MVCC 存储引擎（单进程）。
 *
 * 核心语义：
 * - 每个事务在 begin 时分配单调自增的事务 ID，并固定一致性快照（snapshotTs）。
 * - 读操作基于快照可见性：只能看到「快照时刻之前已提交」的最新版本，
 *   以及事务自己写入的未提交版本（read-your-own-writes）。
 * - 写写冲突采用 First-Committer-Wins：
 *   1) 写时检测：目标键已存在其他事务的未提交版本，或快照之后已有其他事务
 *      提交了新版本 → 当前事务立即 Abort；
 *   2) 提交时校验：快照之后有其他事务提交了相同键 → 首提交者获胜，当前事务 Abort。
 *
 * 实现要点：写入即时把 commitTs=null 的未提交版本挂到版本链上，
 * 使并发事务能互相看见"锁"（未提交版本）从而检测写写冲突；
 * 事务 Abort / Rollback 时摘除自己的未提交版本，提交时原地打上 commitTs。
 */

export type Key = string;
/** null 表示墓碑（删除）。 */
export type Value = string | null;

export interface Version {
  /** 写入该版本的事务 ID。 */
  readonly txnId: number;
  readonly value: Value;
  /** 提交时间戳；null 表示尚未提交。 */
  commitTs: number | null;
}

export type TxnState = 'active' | 'committed' | 'aborted';

export class TransactionAbortedError extends Error {
  readonly txnId: number;

  constructor(txnId: number, message: string) {
    super(message);
    this.name = 'TransactionAbortedError';
    this.txnId = txnId;
  }
}

export interface MvccStoreOptions {
  /** 事务 ID 起始值（第一个事务的 ID），默认 1。 */
  initialTxnId?: number;
}

export class MvccStore {
  private nextTxnId: number;
  private commitSeq = 0;
  /** key -> 版本链（按写入顺序追加，新版本在尾部）。 */
  private readonly data = new Map<Key, Version[]>();

  constructor(options: MvccStoreOptions = {}) {
    this.nextTxnId = options.initialTxnId ?? 1;
  }

  /** 开启事务：分配单调自增 ID，并固定快照。 */
  begin(): Transaction {
    const id = this.nextTxnId++;
    return new Transaction(this, id, this.commitSeq);
  }

  /** 仅供 Transaction 内部使用。 */
  getVersions(key: Key): readonly Version[] {
    return this.data.get(key) ?? [];
  }

  /** 仅供 Transaction 内部使用：把未提交版本挂上版本链。 */
  installUncommitted(key: Key, version: Version): void {
    let chain = this.data.get(key);
    if (!chain) {
      chain = [];
      this.data.set(key, chain);
    }
    chain.push(version);
  }

  /** 仅供 Transaction 内部使用：提交时分配时间戳并原地标记自己的版本。 */
  commitVersions(txnId: number): void {
    const commitTs = ++this.commitSeq;
    for (const chain of this.data.values()) {
      for (const v of chain) {
        if (v.txnId === txnId && v.commitTs === null) {
          v.commitTs = commitTs;
        }
      }
    }
  }

  /** 仅供 Transaction 内部使用：Abort / Rollback 时摘除自己的未提交版本。 */
  removeUncommitted(txnId: number): void {
    for (const [key, chain] of this.data) {
      const kept = chain.filter((v) => !(v.txnId === txnId && v.commitTs === null));
      if (kept.length === 0) this.data.delete(key);
      else if (kept.length !== chain.length) this.data.set(key, kept);
    }
  }

  /** 调试辅助：查看某个键的完整版本链。 */
  inspect(key: Key): readonly Version[] {
    return this.getVersions(key);
  }
}

export class Transaction {
  readonly id: number;
  /** 快照时间戳：begin 时刻已提交的最大 commitTs。 */
  private readonly snapshotTs: number;
  private readonly store: MvccStore;
  private state: TxnState = 'active';
  /** 本事务写过的键（用于提交校验）。 */
  private readonly writtenKeys = new Set<Key>();

  constructor(store: MvccStore, id: number, snapshotTs: number) {
    this.store = store;
    this.id = id;
    this.snapshotTs = snapshotTs;
  }

  get status(): TxnState {
    return this.state;
  }

  /** 快照读：自己的未提交版本优先，否则读快照内已提交的最新版本。 */
  read(key: Key): Value | undefined {
    this.ensureActive('read');
    const chain = this.store.getVersions(key);
    for (let i = chain.length - 1; i >= 0; i--) {
      const v = chain[i];
      if (v.txnId === this.id) return v.value ?? undefined;
      if (v.commitTs !== null && v.commitTs <= this.snapshotTs) {
        return v.value ?? undefined;
      }
    }
    return undefined;
  }

  /**
   * 写操作（First-Committer-Wins 写时检测）：
   * 若目标键存在其他事务的未提交版本，或快照之后已有已提交的新版本，
   * 则当前事务立即 Abort 并抛出 TransactionAbortedError。
   */
  write(key: Key, value: Value): void {
    this.ensureActive('write');
    const chain = this.store.getVersions(key);
    for (let i = chain.length - 1; i >= 0; i--) {
      const v = chain[i];
      if (v.txnId === this.id) break; // 自己的版本在最上面，更旧的无需检查
      if (v.commitTs === null || v.commitTs > this.snapshotTs) {
        this.abort(
          `write-write conflict on key "${key}": first-writer txn ${v.txnId} wins`,
        );
      }
    }
    this.store.installUncommitted(key, { txnId: this.id, value, commitTs: null });
    this.writtenKeys.add(key);
  }

  /** 删除即写入墓碑。 */
  delete(key: Key): void {
    this.write(key, null);
  }

  /**
   * 提交（First-Committer-Wins 提交时校验）：
   * 若写集中的键在快照之后被其他事务提交过，则首提交者获胜，当前事务 Abort。
   */
  commit(): void {
    this.ensureActive('commit');
    for (const key of this.writtenKeys) {
      const chain = this.store.getVersions(key);
      for (const v of chain) {
        if (v.txnId === this.id) continue;
        if (v.commitTs !== null && v.commitTs > this.snapshotTs) {
          this.abort(
            `commit validation failed on key "${key}": first-committer txn ${v.txnId} wins`,
          );
        }
      }
    }
    this.store.commitVersions(this.id);
    this.state = 'committed';
  }

  /** 主动回滚：摘除自己的未提交版本。 */
  rollback(): void {
    this.ensureActive('rollback');
    this.store.removeUncommitted(this.id);
    this.state = 'aborted';
  }

  private abort(reason: string): never {
    this.store.removeUncommitted(this.id);
    this.state = 'aborted';
    throw new TransactionAbortedError(this.id, `txn ${this.id} aborted: ${reason}`);
  }

  private ensureActive(op: string): void {
    if (this.state !== 'active') {
      throw new Error(`cannot ${op}: txn ${this.id} is ${this.state}`);
    }
  }
}
