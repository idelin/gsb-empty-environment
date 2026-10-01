/**
 * 版本向量（Version Vector）
 *
 * 记录每个副本对某个键（或存储）的逻辑写入次数，
 * 用于判断两个版本之间的因果关系：
 *   - equal      完全相同的版本
 *   - before     本向量严格先于对方（被对方支配，可被快照覆盖）
 *   - after      本向量严格晚于对方（支配对方）
 *   - concurrent 双方存在对方不知道的写入 → 分支分叉（Sibling）
 */

export type ReplicaId = string;

export type VectorEntries = Readonly<Record<ReplicaId, number>>;

export type Ordering = 'equal' | 'before' | 'after' | 'concurrent';

export class VersionVector {
  private readonly entries: Map<ReplicaId, number>;

  private constructor(entries: Map<ReplicaId, number>) {
    this.entries = entries;
  }

  static empty(): VersionVector {
    return new VersionVector(new Map());
  }

  static from(entries: VectorEntries): VersionVector {
    const map = new Map<ReplicaId, number>();
    for (const [id, counter] of Object.entries(entries)) {
      if (!Number.isInteger(counter) || counter < 0) {
        throw new Error(`invalid counter for replica "${id}": ${counter}`);
      }
      if (counter > 0) map.set(id, counter);
    }
    return new VersionVector(map);
  }

  /** 返回指定副本的计数，未记录时为 0。 */
  get(id: ReplicaId): number {
    return this.entries.get(id) ?? 0;
  }

  /** 本地写入：递增本副本的计数，返回新向量（不可变）。 */
  tick(id: ReplicaId): VersionVector {
    const map = new Map(this.entries);
    map.set(id, this.get(id) + 1);
    return new VersionVector(map);
  }

  /** 取两个向量的逐分量最大值（事件并集）。 */
  merge(other: VersionVector): VersionVector {
    const map = new Map(this.entries);
    for (const [id, counter] of other.entries) {
      map.set(id, Math.max(this.get(id), counter));
    }
    return new VersionVector(map);
  }

  /** this 是否支配 other（this >= other 且至少一个分量严格更大）。 */
  dominates(other: VersionVector): boolean {
    return this.compare(other) === 'after';
  }

  compare(other: VersionVector): Ordering {
    let greater = false;
    let less = false;
    const ids = new Set([...this.entries.keys(), ...other.entries.keys()]);
    for (const id of ids) {
      const a = this.get(id);
      const b = other.get(id);
      if (a > b) greater = true;
      else if (a < b) less = true;
      if (greater && less) return 'concurrent';
    }
    if (greater) return 'after';
    if (less) return 'before';
    return 'equal';
  }

  isEmpty(): boolean {
    return this.entries.size === 0;
  }

  toJSON(): Record<ReplicaId, number> {
    return Object.fromEntries([...this.entries.entries()].sort());
  }

  toString(): string {
    const body = [...this.entries.entries()]
      .sort()
      .map(([id, n]) => `${id}:${n}`)
      .join(', ');
    return `{${body}}`;
  }
}
