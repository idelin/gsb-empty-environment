/**
 * 自适应批大小（Adaptive Batch Size）调节器。
 *
 * 面向向量化执行引擎的调度组件：根据数据行宽与 L1/L2 缓存命中反馈，
 * 动态调整每个向量批次（Batch）的行数，使单批工作集尽量容纳在 L1 缓存内，
 * 避免缓存颠簸（cache thrashing）与内存带宽瓶颈。
 *
 * 调节规则（默认参数）：
 *  - 行较宽 / L1 命中率过低：批量减半收缩，下限 256；
 *  - 行极窄 / L1 命中率高且有容量余量：批量翻倍放大，上限 4096；
 *  - 批量始终不超过「恰好装满 L1」的容量上界 capacityBound。
 */
export interface CacheStats {
  l1Hits: number;
  l1Misses: number;
  l2Hits: number;
  l2Misses: number;
}

export interface RegulatorOptions {
  /** 批量下限（行较宽时收缩到此值），默认 256 */
  minBatchSize?: number;
  /** 批量上限（行极窄时放大到此值），默认 4096 */
  maxBatchSize?: number;
  /** 初始批量，默认 1024 */
  initialBatchSize?: number;
  /** L1 缓存容量（字节），默认 32 KiB */
  l1CacheBytes?: number;
  /** L2 缓存容量（字节），默认 256 KiB */
  l2CacheBytes?: number;
  /** L1 命中率低水位：低于则判定缓存颠簸并收缩，默认 0.60 */
  l1HitRateLow?: number;
  /** L1 命中率高水位：高于且有容量余量则放大，默认 0.85 */
  l1HitRateHigh?: number;
}

export type AdjustmentReason =
  | "initial"
  | "cache-thrashing-shrink"
  | "capacity-shrink"
  | "grow-into-headroom"
  | "hold";

export interface Adjustment {
  from: number;
  to: number;
  reason: AdjustmentReason;
  l1HitRate: number;
  rowSizeBytes: number;
  capacityBound: number;
}

const KiB = 1024;

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, value));
}

/** 不超过 n 的最大 2 的幂（n < 1 时返回 1）。 */
export function floorPow2(n: number): number {
  if (n < 1) return 1;
  return Math.pow(2, Math.floor(Math.log2(n)));
}

export class AdaptiveBatchSizeRegulator {
  readonly minBatchSize: number;
  readonly maxBatchSize: number;
  readonly l1CacheBytes: number;
  readonly l2CacheBytes: number;
  readonly l1HitRateLow: number;
  readonly l1HitRateHigh: number;
  /** 完整调节历史，便于观测与审计。 */
  readonly history: Adjustment[] = [];

  private batchSize: number;

  constructor(options: RegulatorOptions = {}) {
    this.minBatchSize = options.minBatchSize ?? 256;
    this.maxBatchSize = options.maxBatchSize ?? 4096;
    this.l1CacheBytes = options.l1CacheBytes ?? 32 * KiB;
    this.l2CacheBytes = options.l2CacheBytes ?? 256 * KiB;
    this.l1HitRateLow = options.l1HitRateLow ?? 0.6;
    this.l1HitRateHigh = options.l1HitRateHigh ?? 0.85;
    this.batchSize = clamp(
      options.initialBatchSize ?? 1024,
      this.minBatchSize,
      this.maxBatchSize,
    );
    this.history.push({
      from: this.batchSize,
      to: this.batchSize,
      reason: "initial",
      l1HitRate: 1,
      rowSizeBytes: 0,
      capacityBound: this.batchSize,
    });
  }

  get currentBatchSize(): number {
    return this.batchSize;
  }

  /**
   * 容量上界：给定行宽，恰好装满 L1 缓存的最大批量（向下取 2 的幂，
   * 并夹在 [minBatchSize, maxBatchSize] 内）。
   */
  capacityBoundFor(rowSizeBytes: number): number {
    if (rowSizeBytes <= 0) return this.maxBatchSize;
    const fit = Math.floor(this.l1CacheBytes / rowSizeBytes);
    return clamp(floorPow2(fit), this.minBatchSize, this.maxBatchSize);
  }

  /**
   * 每个批窗口调用一次：输入该窗口的缓存统计与当前行宽，
   * 返回（可能已调整的）新批量。
   */
  observe(stats: CacheStats, rowSizeBytes: number): number {
    const l1Total = stats.l1Hits + stats.l1Misses;
    const l1HitRate = l1Total === 0 ? 1 : stats.l1Hits / l1Total;
    const capacityBound = this.capacityBoundFor(rowSizeBytes);

    const from = this.batchSize;
    let to = from;
    let reason: AdjustmentReason = "hold";

    if (l1HitRate < this.l1HitRateLow && from > capacityBound) {
      // 缓存颠簸（L1 大量 miss，内存带宽受限）：减半收缩，
      // 但不低于「恰好装满 L1」的容量上界，也不低于 minBatchSize。
      to = Math.max(capacityBound, from >> 1);
      reason = to < from ? "cache-thrashing-shrink" : "hold";
    } else if (l1HitRate >= this.l1HitRateHigh && from < capacityBound) {
      // 缓存热态且有容量余量：翻倍放大，减少每行调度开销。
      to = Math.min(capacityBound, from << 1);
      reason = to > from ? "grow-into-headroom" : "hold";
    } else if (from > capacityBound) {
      // 命中率尚可但批量本身超出 L1 容量：直接收缩到容量上界。
      to = capacityBound;
      reason = "capacity-shrink";
    }

    this.batchSize = to;
    this.history.push({ from, to, reason, l1HitRate, rowSizeBytes, capacityBound });
    return to;
  }
}
