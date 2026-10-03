export interface CommittedValue<V> {
  readonly value: V;
  readonly version: number;
}

/**
 * 版本化键值存储。
 *
 * 每个已提交的键携带全局单调递增的提交版本号；
 * 版本 0 保留给“键不存在”的初始状态，用于检测幻读（phantom create）。
 */
export class VersionedStore<K, V> {
  private readonly data = new Map<K, CommittedValue<V>>();
  private commitSeq = 0;

  getCommitted(key: K): CommittedValue<V> | undefined {
    return this.data.get(key);
  }

  versionOf(key: K): number {
    return this.data.get(key)?.version ?? 0;
  }

  nextCommitVersion(): number {
    this.commitSeq += 1;
    return this.commitSeq;
  }

  /** 写入阶段：同一版本号原子地应用整批写入。 */
  applyWrites(entries: Iterable<readonly [K, V]>, version: number): void {
    for (const [key, value] of entries) {
      this.data.set(key, { value, version });
    }
  }

  get size(): number {
    return this.data.size;
  }
}
