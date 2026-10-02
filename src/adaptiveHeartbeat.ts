/**
 * 自适应心跳间隔控制器（发送端）。
 *
 * “自适应衰减”策略（类 AIMD）：
 *  - 网络稳定时：间隔按衰减因子缓慢上调（乘性增大），降低开销 —— 即“衰减”；
 *  - 观测到抖动异常（实际间隔显著偏离历史分布）时：间隔快速下调（乘性减小），
 *    提高采样频率，让 φ 探测器更快积累证据。
 */
export interface AdaptiveHeartbeatOptions {
  /** 初始间隔（ms），默认 1000 */
  initialIntervalMs?: number;
  /** 最小间隔（ms），默认 200 */
  minIntervalMs?: number;
  /** 最大间隔（ms），默认 10_000 */
  maxIntervalMs?: number;
  /** 稳定时的衰减（上调）因子，默认 1.2 */
  decayFactor?: number;
  /** 异常时的收缩（下调）因子，默认 0.5 */
  shrinkFactor?: number;
  /** 判定“异常”的 z-score 阈值（偏离均值的标准差倍数），默认 3 */
  anomalyZScore?: number;
}

export class AdaptiveHeartbeatScheduler {
  private readonly minInterval: number;
  private readonly maxInterval: number;
  private readonly decayFactor: number;
  private readonly shrinkFactor: number;
  private readonly anomalyZ: number;
  private interval: number;

  constructor(options: AdaptiveHeartbeatOptions = {}) {
    this.minInterval = options.minIntervalMs ?? 200;
    this.maxInterval = options.maxIntervalMs ?? 10_000;
    this.decayFactor = options.decayFactor ?? 1.2;
    this.shrinkFactor = options.shrinkFactor ?? 0.5;
    this.anomalyZ = options.anomalyZScore ?? 3;
    this.interval = options.initialIntervalMs ?? 1000;
    this.interval = this.clamp(this.interval);
  }

  get intervalMs(): number {
    return this.interval;
  }

  private clamp(v: number): number {
    return Math.min(this.maxInterval, Math.max(this.minInterval, v));
  }

  /**
   * 根据最近一次心跳的实际到达情况调整下一次发送间隔。
   * @param observedIntervalMs 实际到达间隔（ms）
   * @param expectedMeanMs     历史分布均值（ms）
   * @param expectedStdDevMs   历史分布标准差（ms）
   */
  observe(
    observedIntervalMs: number,
    expectedMeanMs: number,
    expectedStdDevMs: number,
  ): number {
    const deviation = Math.abs(observedIntervalMs - expectedMeanMs);
    const anomalous =
      expectedStdDevMs > 0 && deviation > this.anomalyZ * expectedStdDevMs;

    this.interval = anomalous
      ? this.clamp(this.interval * this.shrinkFactor)
      : this.clamp(this.interval * this.decayFactor);
    return this.interval;
  }

  /** 网络疑似恶化（如连续超时）时立即收缩到最小间隔 */
  escalate(): number {
    this.interval = this.minInterval;
    return this.interval;
  }
}
