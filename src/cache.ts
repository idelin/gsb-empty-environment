/**
 * 模拟的 CPU 缓存层级（L1 / L2），用于向量化执行引擎
 * 在无硬件 PMU 计数器的环境下对缓存命中率进行可观测建模。
 */

export interface CacheSnapshot {
  l1Hits: number;
  l1Misses: number;
  l2Hits: number;
  l2Misses: number;
  memoryReads: number;
}

/** 组相联缓存，LRU 置换策略。 */
export class CacheLevel {
  readonly capacityBytes: number;
  readonly lineSize: number;
  readonly associativity: number;
  private readonly setCount: number;
  private readonly sets: number[][];

  constructor(capacityBytes: number, lineSize: number, associativity: number) {
    this.capacityBytes = capacityBytes;
    this.lineSize = lineSize;
    this.associativity = associativity;
    this.setCount = capacityBytes / (lineSize * associativity);
    this.sets = Array.from({ length: this.setCount }, () => []);
  }

  /** 访问一个字节地址，返回是否命中。 */
  access(address: number): boolean {
    const line = Math.floor(address / this.lineSize);
    const setIndex = line % this.setCount;
    const tag = Math.floor(line / this.setCount);
    const set = this.sets[setIndex];
    const idx = set.indexOf(tag);
    if (idx >= 0) {
      set.splice(idx, 1);
      set.push(tag); // 移到 MRU 端
      return true;
    }
    if (set.length >= this.associativity) set.shift(); // 驱逐 LRU
    set.push(tag);
    return false;
  }
}

export class CacheHierarchy {
  readonly l1: CacheLevel;
  readonly l2: CacheLevel;
  readonly lineSize: number;
  private counters: CacheSnapshot = {
    l1Hits: 0,
    l1Misses: 0,
    l2Hits: 0,
    l2Misses: 0,
    memoryReads: 0,
  };

  constructor(
    l1CapacityBytes = 32 * 1024, // 32KB L1d，典型桌面核
    l2CapacityBytes = 256 * 1024, // 256KB L2
    lineSize = 64,
    l1Associativity = 8,
    l2Associativity = 16,
  ) {
    this.lineSize = lineSize;
    this.l1 = new CacheLevel(l1CapacityBytes, lineSize, l1Associativity);
    this.l2 = new CacheLevel(l2CapacityBytes, lineSize, l2Associativity);
  }

  get l1CapacityBytes(): number {
    return this.l1.capacityBytes;
  }

  access(address: number): void {
    if (this.l1.access(address)) {
      this.counters.l1Hits++;
      return;
    }
    this.counters.l1Misses++;
    if (this.l2.access(address)) {
      this.counters.l2Hits++;
      return;
    }
    this.counters.l2Misses++;
    this.counters.memoryReads++;
  }

  snapshot(): CacheSnapshot {
    return { ...this.counters };
  }

  /** 两个快照之间的增量。 */
  static diff(after: CacheSnapshot, before: CacheSnapshot): CacheSnapshot {
    return {
      l1Hits: after.l1Hits - before.l1Hits,
      l1Misses: after.l1Misses - before.l1Misses,
      l2Hits: after.l2Hits - before.l2Hits,
      l2Misses: after.l2Misses - before.l2Misses,
      memoryReads: after.memoryReads - before.memoryReads,
    };
  }

  static l1HitRate(s: CacheSnapshot): number {
    const total = s.l1Hits + s.l1Misses;
    return total === 0 ? 1 : s.l1Hits / total;
  }

  static l2HitRate(s: CacheSnapshot): number {
    const total = s.l2Hits + s.l2Misses;
    return total === 0 ? 1 : s.l2Hits / total;
  }

  /** 粗略的停顿周期估计：L1 命中 1，L2 命中 10，内存 100。 */
  static estimatedCycles(s: CacheSnapshot): number {
    return (
      (s.l1Hits + s.l1Misses) * 1 + s.l2Hits * 9 + s.memoryReads * 99
    );
  }
}
