/** 向量时钟数据：副本 ID -> 逻辑计数。 */
export type ClockData = Record<string, number>;

/** 两个时钟的偏序关系。 */
export type Ordering = 'before' | 'after' | 'equal' | 'concurrent';

export class VectorClock {
  private readonly entries: Map<string, number>;

  constructor(data?: ClockData) {
    this.entries = new Map();
    if (data) {
      for (const [id, n] of Object.entries(data)) {
        if (n > 0) this.entries.set(id, n);
      }
    }
  }

  static empty(): VectorClock {
    return new VectorClock();
  }

  static fromData(data: ClockData): VectorClock {
    return new VectorClock(data);
  }

  clone(): VectorClock {
    const copy = new VectorClock();
    for (const [id, n] of this.entries) copy.entries.set(id, n);
    return copy;
  }

  get(replicaId: string): number {
    return this.entries.get(replicaId) ?? 0;
  }

  set(replicaId: string, n: number): void {
    if (n <= 0) this.entries.delete(replicaId);
    else this.entries.set(replicaId, n);
  }

  increment(replicaId: string): void {
    this.entries.set(replicaId, this.get(replicaId) + 1);
  }

  /** 归并：逐分量取最大值。 */
  merge(other: VectorClock): void {
    for (const [id, n] of other.entries) {
      if (n > this.get(id)) this.entries.set(id, n);
    }
  }

  merged(other: VectorClock): VectorClock {
    const copy = this.clone();
    copy.merge(other);
    return copy;
  }

  compare(other: VectorClock): Ordering {
    let less = false;
    let greater = false;
    const ids = new Set<string>([...this.entries.keys(), ...other.entries.keys()]);
    for (const id of ids) {
      const a = this.get(id);
      const b = other.get(id);
      if (a < b) less = true;
      else if (a > b) greater = true;
      if (less && greater) return 'concurrent';
    }
    if (less) return 'before';
    if (greater) return 'after';
    return 'equal';
  }

  /** this 是否支配（>=）other，即 other 所标记的事件对持有 this 的一方“已见”。 */
  dominates(other: VectorClock): boolean {
    const order = this.compare(other);
    return order === 'after' || order === 'equal';
  }

  equals(other: VectorClock): boolean {
    return this.compare(other) === 'equal';
  }

  toData(): ClockData {
    const data: ClockData = {};
    for (const [id, n] of [...this.entries].sort()) data[id] = n;
    return data;
  }
}
