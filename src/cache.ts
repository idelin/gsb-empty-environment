/**
 * 字节额度缓存：
 * - 额度按条目实际大小计（key 长度 + 值内容尺寸）；
 * - 分区配额 + 分区间借用，总额度守恒；
 * - 支持按字体精确失效；
 * - 拒绝写入旧代际的条目（配合调度器防止旧回包覆盖新版本）。
 */

export interface CacheMeta {
  readonly fontId?: string;
  readonly fontVersion?: number;
  readonly generation: number;
}

export interface CacheEntry<V = unknown> {
  readonly key: string;
  readonly value: V;
  readonly size: number;
  readonly meta: CacheMeta;
  readonly tick: number;
}

/** 确定性尺寸计算：字符串按字符数，数字/布尔按 8 字节，对象递归求和。 */
export function measureSize(value: unknown): number {
  if (value === null || value === undefined) return 0;
  if (typeof value === 'string') return value.length;
  if (typeof value === 'number' || typeof value === 'boolean') return 8;
  if (Array.isArray(value)) {
    let total = 8;
    for (const item of value) total += measureSize(item);
    return total;
  }
  if (typeof value === 'object') {
    let total = 8;
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      total += k.length + measureSize(v);
    }
    return total;
  }
  return 8;
}

interface Partition {
  quota: number;
  used: number;
  entries: Map<string, CacheEntry>;
}

export interface PartitionStat {
  readonly quota: number;
  readonly used: number;
  readonly count: number;
}

export interface CacheStats {
  readonly totalBudget: number;
  readonly totalQuota: number;
  readonly totalUsed: number;
  readonly partitions: Readonly<Record<string, PartitionStat>>;
  /** 额度守恒：各分区配额之和恒等于总预算。 */
  readonly conserved: boolean;
}

export class ByteCache {
  readonly totalBudget: number;
  private readonly partitions = new Map<string, Partition>();
  private tickCounter = 0;
  /** 写入下限代际：低于该代际的写入一律拒绝。 */
  private writeFloor = 0;

  constructor(totalBudget: number, quotas: Record<string, number>) {
    this.totalBudget = totalBudget;
    let sum = 0;
    for (const [name, quota] of Object.entries(quotas)) {
      this.partitions.set(name, { quota, used: 0, entries: new Map() });
      sum += quota;
    }
    if (sum !== totalBudget) {
      throw new Error(`配额之和 ${sum} 必须等于总预算 ${totalBudget}`);
    }
  }

  /** 提升写入代际下限（热更新后调用，旧代际异步回包无法落盘）。 */
  raiseWriteFloor(generation: number): void {
    if (generation > this.writeFloor) this.writeFloor = generation;
  }

  get currentWriteFloor(): number {
    return this.writeFloor;
  }

  get<V>(partition: string, key: string): CacheEntry<V> | undefined {
    const p = this.mustPartition(partition);
    const entry = p.entries.get(key);
    if (!entry) return undefined;
    // LRU：读后刷新 tick。
    p.entries.delete(key);
    const fresh: CacheEntry = { ...entry, tick: ++this.tickCounter };
    p.entries.set(key, fresh);
    return fresh as CacheEntry<V>;
  }

  peek<V>(partition: string, key: string): CacheEntry<V> | undefined {
    return this.mustPartition(partition).entries.get(key) as CacheEntry<V> | undefined;
  }

  /**
   * 写入条目。大小按实际内容计算。
   * 空间不足时先逐出本分区 LRU，再向有空余配额的分区借用。
   * 返回是否成功落盘。
   */
  set<V>(partition: string, key: string, value: V, meta: CacheMeta): boolean {
    if (meta.generation < this.writeFloor) return false; // 旧代际回包，拒绝
    const p = this.mustPartition(partition);
    const size = measureSize(key) + measureSize(value);

    const existing = p.entries.get(key);
    if (existing) this.removeEntry(p, existing);

    this.evictUntil(p, size);
    if (p.used + size > p.quota) {
      this.borrow(partition, p.used + size - p.quota);
      this.evictUntil(p, size);
    }
    if (p.used + size > p.quota) {
      // 借满仍放不下：还原已有条目不可行（已逐出），按失败处理。
      return false;
    }
    const entry: CacheEntry = { key, value, size, meta, tick: ++this.tickCounter };
    p.entries.set(key, entry);
    p.used += size;
    return true;
  }

  /** 精确失效：仅删除满足条件的条目（如指定 fontId 且版本不等于当前版本）。 */
  invalidateWhere(pred: (meta: CacheMeta, key: string) => boolean): number {
    let removed = 0;
    for (const p of this.partitions.values()) {
      for (const entry of [...p.entries.values()]) {
        if (pred(entry.meta, entry.key)) {
          this.removeEntry(p, entry);
          removed++;
        }
      }
    }
    return removed;
  }

  stats(): CacheStats {
    const partitions: Record<string, PartitionStat> = {};
    let totalQuota = 0;
    let totalUsed = 0;
    for (const [name, p] of this.partitions) {
      partitions[name] = { quota: p.quota, used: p.used, count: p.entries.size };
      totalQuota += p.quota;
      totalUsed += p.used;
    }
    return {
      totalBudget: this.totalBudget,
      totalQuota,
      totalUsed,
      partitions,
      conserved: totalQuota === this.totalBudget && totalUsed <= this.totalBudget,
    };
  }

  private mustPartition(name: string): Partition {
    const p = this.partitions.get(name);
    if (!p) throw new Error(`未知缓存分区: ${name}`);
    return p;
  }

  private removeEntry(p: Partition, entry: CacheEntry): void {
    p.entries.delete(entry.key);
    p.used -= entry.size;
  }

  /** 逐出本分区最久未用条目，直到能容纳 need 字节。 */
  private evictUntil(p: Partition, need: number): void {
    while (p.used + need > p.quota && p.entries.size > 0) {
      let oldest: CacheEntry | undefined;
      for (const entry of p.entries.values()) {
        if (!oldest || entry.tick < oldest.tick) oldest = entry;
      }
      if (!oldest) break;
      this.removeEntry(p, oldest);
    }
  }

  /** 从有空余配额的分区借用额度（只转移配额，不转移字节），保持总额守恒。 */
  private borrow(target: string, need: number): void {
    const tp = this.mustPartition(target);
    let remaining = need;
    const lenders = [...this.partitions.entries()]
      .filter(([name]) => name !== target)
      .map(([name, p]) => ({ name, p, free: p.quota - p.used }))
      .filter((l) => l.free > 0)
      .sort((a, b) => b.free - a.free);
    for (const lender of lenders) {
      if (remaining <= 0) break;
      const amount = Math.min(lender.free, remaining);
      lender.p.quota -= amount;
      tp.quota += amount;
      remaining -= amount;
    }
  }
}
