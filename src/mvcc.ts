/**
 * 内存快照隔离 MVCC 存储引擎
 *
 * - 每个事务分配单调自增的事务 ID
 * - 读操作基于快照可见性：只能看到事务启动时已提交的最新版本
 * - 写写冲突采用首写获胜（First-Committer-Wins），冲突事务被中止
 */

export type Key = string;
export type Value = string;

export type TransactionState = 'active' | 'committed' | 'aborted';

/** 单 key 的一个版本。未提交版本不会进入版本链（写操作先缓冲在事务内）。 */
interface Version {
  /** 写入该版本的事务 ID */
  readonly txnId: number;
  readonly value: Value;
  /** 提交时间戳（提交序列号），单调递增 */
  readonly commitTs: number;
}

export class TransactionNotActiveError extends Error {
  constructor(txnId: number, state: TransactionState) {
    super(`Transaction ${txnId} is not active (state: ${state})`);
    this.name = 'TransactionNotActiveError';
  }
}

export class TransactionAbortedError extends Error {
  readonly txnId: number;

  constructor(txnId: number, reason: string) {
    super(`Transaction ${txnId} aborted: ${reason}`);
    this.name = 'TransactionAbortedError';
    this.txnId = txnId;
  }
}

export class WriteConflictError extends TransactionAbortedError {
  readonly key: Key;

  constructor(txnId: number, key: Key, reason: string) {
    super(txnId, `write-write conflict on key "${key}": ${reason}`);
    this.name = 'WriteConflictError';
    this.key = key;
  }
}

export interface Transaction {
  readonly id: number;
  /** 快照时间戳：事务启动时的提交序列号，仅可见 commitTs <= snapshotTs 的版本 */
  readonly snapshotTs: number;
  readonly state: TransactionState;
  /** 该事务已缓冲的写集合（未提交） */
  readonly writeSet: ReadonlyMap<Key, Value>;
}

interface MutableTransaction {
  readonly id: number;
  readonly snapshotTs: number;
  state: TransactionState;
  readonly writeSet: Map<Key, Value>;
}

export interface MVCCStoreOptions {
  /** 首个事务 ID，默认 1（演示验收场景时可设为 10） */
  firstTxnId?: number;
  /** 预置的已提交数据（作为 commitTs = 0 的引导版本，不占用事务 ID） */
  seed?: Record<Key, Value>;
}

export class MVCCStore {
  private readonly versions = new Map<Key, Version[]>();
  private readonly active = new Map<number, MutableTransaction>();
  /** key -> 持有未提交写的事务 ID（写意图登记，用于写写冲突检测） */
  private readonly writeIntents = new Map<Key, number>();
  private lastTxnId: number;
  private commitCounter = 0;

  constructor(options: MVCCStoreOptions = {}) {
    this.lastTxnId = (options.firstTxnId ?? 1) - 1;
    if (options.seed) {
      for (const [key, value] of Object.entries(options.seed)) {
        this.chain(key).push({ txnId: 0, value, commitTs: 0 });
      }
    }
  }

  /** 开启事务：分配单调自增事务 ID，并固定其读快照。 */
  begin(): Transaction {
    const txn: MutableTransaction = {
      id: ++this.lastTxnId,
      snapshotTs: this.commitCounter,
      state: 'active',
      writeSet: new Map(),
    };
    this.active.set(txn.id, txn);
    return txn;
  }

  /** 基于快照可见性读取：优先读自己的未提交写，否则读快照内最新已提交版本。 */
  read(tx: Transaction, key: Key): Value | undefined {
    const txn = this.requireActive(tx);
    if (txn.writeSet.has(key)) return txn.writeSet.get(key);

    const chain = this.versions.get(key);
    if (!chain) return undefined;
    let latest: Version | undefined;
    for (const v of chain) {
      if (v.commitTs <= txn.snapshotTs && (!latest || v.commitTs > latest.commitTs)) {
        latest = v;
      }
    }
    return latest?.value;
  }

  /**
   * 写入（缓冲在事务内，提交时才落入版本链）。
   * 写写冲突检测（首写获胜）：
   *  1. 其他活跃事务已持有该 key 的未提交写 -> 本事务中止；
   *  2. 该 key 在本事务快照之后已有新提交版本 -> 本事务中止。
   */
  write(tx: Transaction, key: Key, value: Value): void {
    const txn = this.requireActive(tx);

    const holder = this.writeIntents.get(key);
    if (holder !== undefined && holder !== txn.id) {
      this.abort(txn);
      throw new WriteConflictError(
        txn.id,
        key,
        `key is locked by uncommitted write of transaction ${holder}`,
      );
    }

    const latestCommitted = this.latestCommitted(key);
    if (latestCommitted && latestCommitted.commitTs > txn.snapshotTs) {
      this.abort(txn);
      throw new WriteConflictError(
        txn.id,
        key,
        `key was committed by transaction ${latestCommitted.txnId} after this transaction's snapshot`,
      );
    }

    txn.writeSet.set(key, value);
    this.writeIntents.set(key, txn.id);
  }

  /**
   * 提交：再次校验首写获胜（快照之后无他人提交同一 key），
   * 通过则为所有写分配统一提交时间戳并追加到版本链。
   */
  commit(tx: Transaction): void {
    const txn = this.requireActive(tx);
    try {
      for (const key of txn.writeSet.keys()) {
        const latestCommitted = this.latestCommitted(key);
        if (latestCommitted && latestCommitted.commitTs > txn.snapshotTs) {
          throw new WriteConflictError(
            txn.id,
            key,
            `key was committed by transaction ${latestCommitted.txnId} after this transaction's snapshot`,
          );
        }
      }
      const commitTs = ++this.commitCounter;
      for (const [key, value] of txn.writeSet) {
        this.chain(key).push({ txnId: txn.id, value, commitTs });
      }
      txn.state = 'committed';
    } catch (err) {
      txn.state = 'aborted';
      throw err;
    } finally {
      this.release(txn);
    }
  }

  /** 中止事务：丢弃缓冲写并释放写意图。 */
  abort(tx: Transaction): void {
    const txn = this.active.get(tx.id);
    if (!txn || txn.state !== 'active') return;
    txn.state = 'aborted';
    this.release(txn);
  }

  stateOf(tx: Transaction): TransactionState {
    return this.active.get(tx.id)?.state ?? tx.state;
  }

  private requireActive(tx: Transaction): MutableTransaction {
    const txn = this.active.get(tx.id);
    if (!txn || txn.state !== 'active') {
      throw new TransactionNotActiveError(tx.id, this.stateOf(tx));
    }
    return txn;
  }

  private release(txn: MutableTransaction): void {
    for (const key of txn.writeSet.keys()) {
      if (this.writeIntents.get(key) === txn.id) this.writeIntents.delete(key);
    }
    this.active.delete(txn.id);
  }

  private chain(key: Key): Version[] {
    let c = this.versions.get(key);
    if (!c) {
      c = [];
      this.versions.set(key, c);
    }
    return c;
  }

  private latestCommitted(key: Key): Version | undefined {
    const chain = this.versions.get(key);
    return chain && chain.length > 0 ? chain[chain.length - 1] : undefined;
  }
}
