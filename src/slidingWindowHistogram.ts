/**
 * SlidingWindowHistogram
 * ----------------------
 * 滑动窗口直方图 + 高百分位数（P90/P99）估算器。
 *
 * 设计要点：
 * 1. 混合分桶：低值区 [0, fixedRange) 使用固定桶宽线性桶；
 *    高值区 [fixedRange, maxValue] 使用对数桶（相邻桶比值为 logBase）。
 *    因此低值区误差是绝对误差（<= fixedBucketWidth/2），
 *    高值区误差是相对误差（<= (logBase-1)/2），整体误差有理论界限。
 * 2. 滑动窗口：容量为 capacity 的环形缓冲区，每个槽位只保存样本的
 *    "桶索引"（Uint32），不保存原始明细样本。窗口满时新样本顶掉最老样本，
 *    并对最老样本所在桶做计数减一（反向补偿），实现 O(1) 过期。
 * 3. 百分位数：nearest-rank 方式在桶计数上累计扫描，返回所在桶的代表值
 *    （线性桶取中点，对数桶取几何中点），时间复杂度 O(桶数)，空间 O(桶数+容量)。
 */

export interface SlidingWindowHistogramOptions {
  /** 滑动窗口容量（样本个数），默认 1000 */
  capacity?: number;
  /** 低值区固定桶宽，默认 0.1（与 value 同单位，例如毫秒） */
  fixedBucketWidth?: number;
  /** 低值区上界，[0, fixedRange) 使用固定桶，默认 1000 */
  fixedRange?: number;
  /** 高值区对数桶相邻比值，默认 1.005（相对分辨率约 0.5%） */
  logBase?: number;
  /** 可记录的最大值，超出将被钳制到最后一个桶，默认 3_600_000（1 小时，毫秒单位时） */
  maxValue?: number;
}

export class SlidingWindowHistogram {
  readonly capacity: number;
  readonly fixedBucketWidth: number;
  readonly fixedRange: number;
  readonly logBase: number;
  readonly maxValue: number;

  private readonly fixedBucketCount: number;
  private readonly logBucketCount: number;
  private readonly bucketCount: number;
  private readonly counts: Uint32Array;
  private readonly slots: Uint32Array;
  private head = 0;
  private size = 0;
  private lifetimeCount = 0;

  constructor(options: SlidingWindowHistogramOptions = {}) {
    this.capacity = options.capacity ?? 1000;
    this.fixedBucketWidth = options.fixedBucketWidth ?? 0.1;
    this.fixedRange = options.fixedRange ?? 1000;
    this.logBase = options.logBase ?? 1.005;
    this.maxValue = options.maxValue ?? 3_600_000;

    if (!(this.capacity > 0)) throw new Error("capacity must be positive");
    if (!(this.fixedBucketWidth > 0)) throw new Error("fixedBucketWidth must be positive");
    if (!(this.fixedRange > 0)) throw new Error("fixedRange must be positive");
    if (!(this.logBase > 1)) throw new Error("logBase must be > 1");
    if (!(this.maxValue > this.fixedRange)) throw new Error("maxValue must exceed fixedRange");

    this.fixedBucketCount = Math.ceil(this.fixedRange / this.fixedBucketWidth);
    this.logBucketCount =
      Math.ceil(Math.log(this.maxValue / this.fixedRange) / Math.log(this.logBase)) + 1;
    this.bucketCount = this.fixedBucketCount + this.logBucketCount;

    this.counts = new Uint32Array(this.bucketCount);
    this.slots = new Uint32Array(this.capacity);
  }

  /** 当前窗口内样本数（<= capacity） */
  get windowSize(): number {
    return this.size;
  }

  /** 自创建以来累计记录的样本数（含已过期） */
  get totalRecorded(): number {
    return this.lifetimeCount;
  }

  /** 桶总数（内存占用 O(bucketCount + capacity)） */
  get buckets(): number {
    return this.bucketCount;
  }

  /** 高值区相对误差理论界限：(logBase - 1) / 2 */
  get maxRelativeError(): number {
    return (this.logBase - 1) / 2;
  }

  /** 低值区绝对误差理论界限：fixedBucketWidth / 2 */
  get maxAbsoluteError(): number {
    return this.fixedBucketWidth / 2;
  }

  /** 记录一个样本；窗口满时自动过期最老样本并做反向补偿。O(1) */
  record(value: number): void {
    if (!Number.isFinite(value)) throw new Error("value must be finite");
    const index = this.bucketIndex(value);

    if (this.size === this.capacity) {
      // 反向补偿：最老样本滑出窗口，其桶计数减一
      this.counts[this.slots[this.head]]!--;
    } else {
      this.size++;
    }

    this.slots[this.head] = index;
    this.counts[index]!++;
    this.head = (this.head + 1) % this.capacity;
    this.lifetimeCount++;
  }

  /**
   * 估算分位数。q ∈ (0, 1]，nearest-rank。
   * 返回所在桶的代表值；窗口为空时返回 NaN。
   */
  quantile(q: number): number {
    if (!(q > 0 && q <= 1)) throw new Error("q must be in (0, 1]");
    if (this.size === 0) return NaN;

    const rank = Math.max(1, Math.ceil(q * this.size));
    let cumulative = 0;
    for (let i = 0; i < this.bucketCount; i++) {
      cumulative += this.counts[i]!;
      if (cumulative >= rank) return this.bucketEstimate(i);
    }
    return this.bucketEstimate(this.bucketCount - 1); // 不可达，防御
  }

  get p90(): number {
    return this.quantile(0.9);
  }

  get p99(): number {
    return this.quantile(0.99);
  }

  /** 窗口内所有桶计数之和（应恒等于 windowSize，用于校验反向补偿正确性） */
  countSum(): number {
    let sum = 0;
    for (let i = 0; i < this.bucketCount; i++) sum += this.counts[i]!;
    return sum;
  }

  /** 指定桶的当前计数（测试/调试用） */
  countAt(bucketIndex: number): number {
    return this.counts[bucketIndex]!;
  }

  /** 值 -> 桶索引 */
  bucketIndex(value: number): number {
    const v = Math.min(Math.max(value, 0), this.maxValue);
    if (v < this.fixedRange) {
      return Math.min(Math.floor(v / this.fixedBucketWidth), this.fixedBucketCount - 1);
    }
    const logIdx = Math.floor(Math.log(v / this.fixedRange) / Math.log(this.logBase));
    return Math.min(this.fixedBucketCount + logIdx, this.bucketCount - 1);
  }

  /** 桶下界 */
  bucketLowerBound(index: number): number {
    if (index < this.fixedBucketCount) return index * this.fixedBucketWidth;
    return this.fixedRange * Math.pow(this.logBase, index - this.fixedBucketCount);
  }

  /** 桶上界（开区间） */
  bucketUpperBound(index: number): number {
    if (index < this.fixedBucketCount) return (index + 1) * this.fixedBucketWidth;
    return this.fixedRange * Math.pow(this.logBase, index - this.fixedBucketCount + 1);
  }

  /** 桶代表值：线性桶取算术中点，对数桶取几何中点 */
  bucketEstimate(index: number): number {
    if (index < this.fixedBucketCount) {
      return (index + 0.5) * this.fixedBucketWidth;
    }
    const lower = this.bucketLowerBound(index);
    return lower * Math.sqrt(this.logBase);
  }
}
