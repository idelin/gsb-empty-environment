/**
 * 自适应批大小调节器（Adaptive Batch Size Regulator）。
 *
 * 策略：
 *  - 静态维度：根据行宽（rowSizeBytes）计算"刚好容纳进 L1"的最大 2 次幂批大小，
 *    并夹在 [minBatchSize, maxBatchSize] = [256, 4096] 之间；
 *  - 动态维度：每个批处理完成后观察 L1 命中率，
 *    命中率低于 shrinkThreshold（缓存颠簸 / 内存带宽受限）则减半，
 *    命中率高于 growThreshold 且尚未达到静态目标则翻倍。
 */

export interface RegulatorConfig {
  l1CapacityBytes?: number;
  minBatchSize?: number;
  maxBatchSize?: number;
  initialBatchSize?: number;
  /** L1 命中率低于该值视为缓存颠簸，收缩批大小。 */
  shrinkThreshold?: number;
  /** L1 命中率高于该值且未达目标时，放大批大小。 */
  growThreshold?: number;
}

export interface BatchObservation {
  batchIndex: number;
  rows: number;
  l1HitRate: number;
  l2HitRate: number;
}

export type RegulatorAction = "shrink" | "grow" | "hold";

export interface RegulatorDecision {
  batchIndex: number;
  observedL1HitRate: number;
  previousBatchSize: number;
  batchSize: number;
  action: RegulatorAction;
  targetBatchSize: number;
}

const DEFAULTS = {
  l1CapacityBytes: 32 * 1024,
  minBatchSize: 256,
  maxBatchSize: 4096,
  initialBatchSize: 1024,
  shrinkThreshold: 0.6,
  growThreshold: 0.7,
} as const;

export class AdaptiveBatchSizeRegulator {
  readonly config: {
    l1CapacityBytes: number;
    minBatchSize: number;
    maxBatchSize: number;
    initialBatchSize: number;
    shrinkThreshold: number;
    growThreshold: number;
  };
  private currentBatchSize: number;
  private rowSizeBytes = 0;
  readonly decisions: RegulatorDecision[] = [];

  constructor(config: RegulatorConfig = {}) {
    this.config = { ...DEFAULTS, ...config };
    if (this.config.minBatchSize > this.config.maxBatchSize) {
      throw new Error("minBatchSize 不能大于 maxBatchSize");
    }
    this.currentBatchSize = this.clamp(this.config.initialBatchSize);
  }

  get batchSize(): number {
    return this.currentBatchSize;
  }

  get rowSize(): number {
    return this.rowSizeBytes;
  }

  /** 执行引擎在查询开始时上报行宽（字节）。 */
  setRowSize(rowSizeBytes: number): void {
    if (!Number.isFinite(rowSizeBytes) || rowSizeBytes <= 0) {
      throw new Error(`非法行宽: ${rowSizeBytes}`);
    }
    this.rowSizeBytes = rowSizeBytes;
  }

  /**
   * 静态目标批大小：单批足迹（batchSize * rowSize）不超过 L1 容量的
   * 最大 2 的幂，并夹在 [minBatchSize, maxBatchSize]。
   * 行极窄时放大到 4096，行较宽时收缩到 256。
   */
  targetBatchSize(): number {
    if (this.rowSizeBytes === 0) return this.currentBatchSize;
    const fitting = Math.floor(this.config.l1CapacityBytes / this.rowSizeBytes);
    const pow2 = fitting <= 1 ? 1 : 2 ** Math.floor(Math.log2(fitting));
    return this.clamp(pow2);
  }

  /** 单批数据足迹（字节）。 */
  footprintBytes(batchSize = this.currentBatchSize): number {
    return batchSize * this.rowSizeBytes;
  }

  /** 每个批处理完成后由执行引擎回调，返回（可能调整后的）批大小。 */
  observe(observation: BatchObservation): number {
    const previous = this.currentBatchSize;
    const target = this.targetBatchSize();
    let action: RegulatorAction = "hold";

    if (
      observation.l1HitRate < this.config.shrinkThreshold &&
      this.currentBatchSize > this.config.minBatchSize
    ) {
      // 缓存颠簸 / 内存带宽受限：减半收缩
      this.currentBatchSize = Math.max(
        this.config.minBatchSize,
        this.currentBatchSize >> 1,
      );
      action = "shrink";
    } else if (
      observation.l1HitRate >= this.config.growThreshold &&
      this.currentBatchSize < Math.min(this.config.maxBatchSize, target)
    ) {
      // 缓存热态良好且行足够窄：翻倍放大
      this.currentBatchSize = Math.min(
        this.config.maxBatchSize,
        target,
        this.currentBatchSize << 1,
      );
      action = "grow";
    }

    this.decisions.push({
      batchIndex: observation.batchIndex,
      observedL1HitRate: observation.l1HitRate,
      previousBatchSize: previous,
      batchSize: this.currentBatchSize,
      action,
      targetBatchSize: target,
    });
    return this.currentBatchSize;
  }

  private clamp(size: number): number {
    return Math.min(
      this.config.maxBatchSize,
      Math.max(this.config.minBatchSize, size),
    );
  }
}
