/**
 * MVCC 事务状态库（无外部依赖）。
 *
 * 核心语义：
 * - 每个键保存一条按提交版本升序排列的版本链，读事务/快照在开始时被固定在某个
 *   全局版本上，只能看到该版本及之前已提交的值（快照隔离）。
 * - 写事务的写入先缓存在事务私有的 writeSet 中，提交前对其他读者完全不可见，
 *   因此读者永远看不到事务的中间态。
 * - 提交时校验读集：读过的键若已被其他事务提交了新版本，则提交失败并原子回滚
 *   （缓冲的写入直接丢弃，存储状态不受任何影响），从而避免静默丢失更新。
 * - 每次成功提交追加一条日志记录（journal），可通过 Store.replay 完整回放。
 * - 活跃读事务/快照会"钉住"其读版本；gc() 只回收所有活跃读者都看不到的旧版本。
 */

export type Key = string;
export type Version = number;

/** 键不存在时在读集中记录的版本号。 */
export const ABSENT: Version = 0;

export interface VersionedValue<V = unknown> {
  readonly version: Version;
  readonly value: V;
  readonly txnId: number;
}

/** 一条已提交的写操作日志，可序列化、可回放。 */
export interface JournalEntry<V = unknown> {
  readonly txnId: number;
  readonly commitVersion: Version;
  readonly writes: ReadonlyArray<readonly [Key, V]>;
}

export class TransactionConflictError extends Error {
  readonly key: Key;
  readonly expected: Version;
  readonly actual: Version;

  constructor(key: Key, expected: Version, actual: Version) {
    super(
      `commit conflict on key "${key}": read at version ${expected}, ` +
        `but current version is ${actual}`,
    );
    this.name = "TransactionConflictError";
    this.key = key;
    this.expected = expected;
    this.actual = actual;
  }
}

export class TransactionStateError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TransactionStateError";
  }
}

export class Store {
  private readonly chains = new Map<Key, VersionedValue[]>();
  private readonly journal: JournalEntry[] = [];
  private readonly pinned = new Map<Version, number>();
  private commitVersion: Version = 0;
  private nextTxnId = 1;

  /** 当前全局提交版本（单调递增）。 */
  get currentVersion(): Version {
    return this.commitVersion;
  }

  /** 开启一个读写事务，读视图固定在开启瞬间的提交版本。 */
  begin(): Transaction {
    return new Transaction(this, this.nextTxnId++);
  }

  /** 开启一个只读快照，用完必须 release（或使用 using 自动释放）。 */
  snapshot(): Snapshot {
    return new Snapshot(this, this.commitVersion);
  }

  /**
   * 垃圾回收：删除所有活跃读者（事务/快照）都看不到的旧版本。
   * 对每个键，保留每个钉住版本（及当前最新版本）实际可见的那一个版本，
   * 其余版本没有任何读者能看到，安全回收。
   * 返回回收掉的版本个数。
   */
  gc(): number {
    const readers: Version[] = [...this.pinned.keys(), this.commitVersion];
    let collected = 0;
    for (const chain of this.chains.values()) {
      const keep = new Array<boolean>(chain.length).fill(false);
      for (const reader of readers) {
        // 二分查找该读者可见的条目：最后一个 version <= reader 的版本。
        let lo = 0;
        let hi = chain.length - 1;
        let visible = -1;
        while (lo <= hi) {
          const mid = (lo + hi) >> 1;
          if (chain[mid].version <= reader) {
            visible = mid;
            lo = mid + 1;
          } else {
            hi = mid - 1;
          }
        }
        if (visible >= 0) keep[visible] = true;
      }
      let write = 0;
      for (let i = 0; i < chain.length; i++) {
        if (keep[i]) chain[write++] = chain[i];
        else collected++;
      }
      chain.length = write;
    }
    return collected;
  }

  /** 返回操作日志的深拷贝，可持久化后用于回放。 */
  getJournal(): JournalEntry[] {
    return this.journal.map((entry) => ({
      txnId: entry.txnId,
      commitVersion: entry.commitVersion,
      writes: entry.writes.map(
        ([key, value]) => [key, structuredClone(value)] as const,
      ),
    }));
  }

  /** 从操作日志回放出一个新存储，重放所有已提交的写操作。 */
  static replay(journal: Iterable<JournalEntry>): Store {
    const store = new Store();
    for (const entry of journal) {
      store.applyCommitted(entry);
    }
    return store;
  }

  /** 读取某个键在指定版本下可见的值（内部使用）。 */
  readAt(key: Key, version: Version): unknown {
    const chain = this.chains.get(key);
    if (!chain) return undefined;
    for (let i = chain.length - 1; i >= 0; i--) {
      if (chain[i].version <= version) return chain[i].value;
    }
    return undefined;
  }

  /** 键当前最新已提交版本，不存在时为 ABSENT（内部使用）。 */
  latestVersion(key: Key): Version {
    const chain = this.chains.get(key);
    return chain && chain.length ? chain[chain.length - 1].version : ABSENT;
  }

  /** 键在指定版本下可见条目的版本号，不可见时为 ABSENT（内部使用）。 */
  versionAt(key: Key, version: Version): Version {
    const chain = this.chains.get(key);
    if (!chain) return ABSENT;
    for (let i = chain.length - 1; i >= 0; i--) {
      if (chain[i].version <= version) return chain[i].version;
    }
    return ABSENT;
  }

  pin(version: Version): void {
    this.pinned.set(version, (this.pinned.get(version) ?? 0) + 1);
  }

  unpin(version: Version): void {
    const count = this.pinned.get(version);
    if (count === undefined) return;
    if (count <= 1) this.pinned.delete(version);
    else this.pinned.set(version, count - 1);
  }

  /** 校验读集并原子应用写集（内部使用，仅由 Transaction.commit 调用）。 */
  commitTransaction(txn: Transaction): Version {
    for (const [key, expected] of txn.readEntries()) {
      const actual = this.latestVersion(key);
      if (actual !== expected) {
        throw new TransactionConflictError(key, expected, actual);
      }
    }
    const entry: JournalEntry = {
      txnId: txn.id,
      commitVersion: ++this.commitVersion,
      writes: [...txn.writeEntries()].map(
        ([key, value]) => [key, structuredClone(value)] as const,
      ),
    };
    for (const [key, value] of entry.writes) {
      let chain = this.chains.get(key);
      if (!chain) {
        chain = [];
        this.chains.set(key, chain);
      }
      chain.push({ version: entry.commitVersion, value, txnId: txn.id });
    }
    this.journal.push(entry);
    return entry.commitVersion;
  }

  private applyCommitted(entry: JournalEntry): void {
    for (const [key, value] of entry.writes) {
      let chain = this.chains.get(key);
      if (!chain) {
        chain = [];
        this.chains.set(key, chain);
      }
      chain.push({
        version: entry.commitVersion,
        value: structuredClone(value),
        txnId: entry.txnId,
      });
    }
    this.journal.push({
      txnId: entry.txnId,
      commitVersion: entry.commitVersion,
      writes: entry.writes.map(
        ([key, value]) => [key, structuredClone(value)] as const,
      ),
    });
    this.commitVersion = Math.max(this.commitVersion, entry.commitVersion);
    this.nextTxnId = Math.max(this.nextTxnId, entry.txnId + 1);
  }
}

export class Transaction {
  readonly id: number;
  /** 本事务的读视图所固定的版本。 */
  readonly readVersion: Version;

  private readonly store: Store;
  private readonly readSet = new Map<Key, Version>();
  private readonly writeSet = new Map<Key, unknown>();
  private state: "active" | "committed" | "aborted" = "active";

  constructor(store: Store, id: number) {
    this.store = store;
    this.id = id;
    this.readVersion = store.currentVersion;
    store.pin(this.readVersion);
  }

  get isActive(): boolean {
    return this.state === "active";
  }

  /** 读取键：先命中本事务的写缓冲（读己之写），否则读固定版本下的快照值。 */
  get(key: Key): unknown {
    this.assertActive();
    if (this.writeSet.has(key)) return this.writeSet.get(key);
    const value = this.store.readAt(key, this.readVersion);
    if (!this.readSet.has(key)) {
      this.readSet.set(key, this.versionOf(key));
    }
    return value;
  }

  /** 缓冲一个写入；提交前对其他事务/快照不可见。 */
  set(key: Key, value: unknown): void {
    this.assertActive();
    this.writeSet.set(key, value);
  }

  /**
   * 提交：先校验读集版本，冲突则原子回滚并抛 TransactionConflictError；
   * 成功则原子地应用全部写入并返回新的提交版本。
   */
  commit(): Version {
    this.assertActive();
    try {
      if (this.writeSet.size === 0) {
        this.state = "committed";
        return this.readVersion;
      }
      const version = this.store.commitTransaction(this);
      this.state = "committed";
      return version;
    } catch (error) {
      // 冲突：写缓冲从未触碰存储，丢弃即完成原子回滚。
      this.state = "aborted";
      this.writeSet.clear();
      throw error;
    } finally {
      this.store.unpin(this.readVersion);
    }
  }

  /** 主动回滚：丢弃全部缓冲写入，存储不受任何影响。 */
  rollback(): void {
    if (this.state !== "active") return;
    this.state = "aborted";
    this.writeSet.clear();
    this.store.unpin(this.readVersion);
  }

  /** @internal */
  readEntries(): IterableIterator<[Key, Version]> {
    return this.readSet.entries();
  }

  /** @internal */
  writeEntries(): IterableIterator<[Key, unknown]> {
    return this.writeSet.entries();
  }

  private versionOf(key: Key): Version {
    return this.store.versionAt(key, this.readVersion);
  }

  private assertActive(): void {
    if (this.state !== "active") {
      throw new TransactionStateError(
        `transaction ${this.id} is already ${this.state}`,
      );
    }
  }
}

export class Snapshot {
  /** 快照固定的版本。 */
  readonly version: Version;

  private readonly store: Store;
  private released = false;

  constructor(store: Store, version: Version) {
    this.store = store;
    this.version = version;
    store.pin(version);
  }

  /** 读取固定版本下的值；永远看不到该版本之后的提交。 */
  get(key: Key): unknown {
    if (this.released) {
      throw new TransactionStateError("snapshot is already released");
    }
    return this.store.readAt(key, this.version);
  }

  /** 释放快照，解除版本钉住，使其旧版本可被 gc 回收。 */
  release(): void {
    if (this.released) return;
    this.released = true;
    this.store.unpin(this.version);
  }

  [Symbol.dispose](): void {
    this.release();
  }
}
