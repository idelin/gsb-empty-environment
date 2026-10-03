/**
 * 用于仿真与测试的 CPU 缓存层级模型（L1 -> L2 -> 内存）。
 * 以缓存行（cache line，默认 64B）为粒度的 LRU 模型，
 * 用于复现「工作集超出 L1 导致颠簸」的行为。
 */
import type { CacheStats } from "./regulator";

export class LruCache {
  private readonly lines = new Map<number, true>();
  private readonly capacityLines: number;

  constructor(
    readonly capacityBytes: number,
    readonly lineBytes: number = 64,
  ) {
    this.capacityLines = Math.max(1, Math.floor(capacityBytes / lineBytes));
  }

  /** 访问一个缓存行，返回是否命中。 */
  access(line: number): boolean {
    if (this.lines.has(line)) {
      // LRU：命中后移到最新位置。
      this.lines.delete(line);
      this.lines.set(line, true);
      return true;
    }
    this.lines.set(line, true);
    if (this.lines.size > this.capacityLines) {
      const oldest = this.lines.keys().next().value as number;
      this.lines.delete(oldest);
    }
    return false;
  }

  clear(): void {
    this.lines.clear();
  }
}

export interface CacheCosts {
  l1HitCycles: number;
  l2HitCycles: number;
  memoryCycles: number;
}

export const DEFAULT_COSTS: CacheCosts = {
  l1HitCycles: 1,
  l2HitCycles: 14,
  memoryCycles: 100,
};

export class CacheHierarchy {
  readonly l1: LruCache;
  readonly l2: LruCache;
  readonly costs: CacheCosts;
  private stats: CacheStats = { l1Hits: 0, l1Misses: 0, l2Hits: 0, l2Misses: 0 };

  constructor(
    l1Bytes: number = 32 * 1024,
    l2Bytes: number = 256 * 1024,
    readonly lineBytes: number = 64,
    costs: CacheCosts = DEFAULT_COSTS,
  ) {
    this.l1 = new LruCache(l1Bytes, lineBytes);
    this.l2 = new LruCache(l2Bytes, lineBytes);
    this.costs = costs;
  }

  /** 顺序访问一段字节区间，按缓存行粒度计入 L1/L2 命中统计。 */
  accessRange(startByte: number, lengthBytes: number): void {
    const first = Math.floor(startByte / this.lineBytes);
    const last = Math.floor((startByte + Math.max(1, lengthBytes) - 1) / this.lineBytes);
    for (let line = first; line <= last; line++) {
      if (this.l1.access(line)) {
        this.stats.l1Hits++;
      } else {
        this.stats.l1Misses++;
        if (this.l2.access(line)) {
          this.stats.l2Hits++;
        } else {
          this.stats.l2Misses++;
        }
      }
    }
  }

  snapshot(): CacheStats {
    return { ...this.stats };
  }

  resetStats(): void {
    this.stats = { l1Hits: 0, l1Misses: 0, l2Hits: 0, l2Misses: 0 };
  }

  /** 估算该统计窗口消耗的 CPU 周期数。 */
  estimateCycles(stats: CacheStats): number {
    return (
      stats.l1Hits * this.costs.l1HitCycles +
      stats.l2Hits * this.costs.l2HitCycles +
      stats.l2Misses * this.costs.memoryCycles
    );
  }
}
