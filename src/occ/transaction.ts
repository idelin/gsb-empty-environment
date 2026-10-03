import type { VersionedStore } from './store.ts';

interface ReadRecord<V> {
  readonly version: number;
  readonly value: V | undefined;
}

export type TxnState = 'active' | 'committed' | 'aborted';

/**
 * 读取阶段的事务上下文。
 *
 * - ReadSet：键 -> 读取时观察到的提交版本（不存在的键记录版本 0）
 * - WriteSet：键 -> 待写入的新值（提交前对其他事务不可见）
 */
export class OccTransaction<K, V> {
  private readonly readSet = new Map<K, ReadRecord<V>>();
  private readonly writeSet = new Map<K, V>();
  private state: TxnState = 'active';
  private readonly store: VersionedStore<K, V>;

  constructor(store: VersionedStore<K, V>) {
    this.store = store;
  }

  /** 读取阶段：无锁、不阻塞；优先读自己的 WriteSet（read-your-writes）。 */
  get(key: K): V | undefined {
    this.assertActive();
    if (this.writeSet.has(key)) {
      return this.writeSet.get(key);
    }
    const cached = this.readSet.get(key);
    if (cached !== undefined) {
      return cached.value;
    }
    const committed = this.store.getCommitted(key);
    const record: ReadRecord<V> = committed === undefined
      ? { version: 0, value: undefined }
      : { version: committed.version, value: committed.value };
    this.readSet.set(key, record);
    return record.value;
  }

  /** 写入仅缓冲在 WriteSet 中，直到写入阶段一次性批量提交。 */
  set(key: K, value: V): void {
    this.assertActive();
    this.writeSet.set(key, value);
  }

  /** 验证阶段：返回 ReadSet 中版本已过期的键（空数组表示无冲突）。 */
  validate(): K[] {
    const conflicts: K[] = [];
    for (const [key, record] of this.readSet) {
      if (this.store.versionOf(key) !== record.version) {
        conflicts.push(key);
      }
    }
    return conflicts;
  }

  get readSize(): number {
    return this.readSet.size;
  }

  get writeSize(): number {
    return this.writeSet.size;
  }

  get txnState(): TxnState {
    return this.state;
  }

  writeSetEntries(): IterableIterator<[K, V]> {
    return this.writeSet.entries();
  }

  markCommitted(): void {
    this.state = 'committed';
  }

  markAborted(): void {
    this.state = 'aborted';
  }

  private assertActive(): void {
    if (this.state !== 'active') {
      throw new Error(`transaction is already ${this.state}`);
    }
  }
}
