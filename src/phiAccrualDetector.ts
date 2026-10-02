import { normalCdf } from "./statistics.ts";
import { HeartbeatWindow } from "./heartbeatWindow.ts";

export interface PhiDetectorOptions {
  /** 滑动窗口大小（保留的历史心跳间隔样本数），默认 1000 */
  windowSize?: number;
  /** 样本标准差下限（ms），防止分布过窄导致 φ 爆炸，默认 10ms */
  minStdDeviationMs?: number;
  /** 可容忍的心跳停顿（ms），从延迟中扣除，相当于“礼貌期”，默认 0 */
  acceptablePauseMs?: number;
  /** 样本不足时的预热样本数：样本少于此数时暂不判定失效，默认 10 */
  warmupSamples?: number;
  /** 时钟，默认 Date.now；测试可注入虚拟时钟 */
  now?: () => number;
}

/**
 * φ-Accrual 失效探测器（仿 Cassandra 实现）。
 *
 * 不采用固定超时阈值，而是基于历史心跳间隔的经验分布，
 * 计算“当前等待时长之后仍会收到心跳”的概率 P_later(t)，
 * 怀疑度 φ(t) = -log10(P_later(t))。
 *
 * φ 每增大 1，误判概率下降一个数量级：
 *   φ=1 → 约 10% 误判，φ=8 → 约 1e-8 误判（常用阈值 8 / 12 / 16）。
 */
export class PhiAccrualDetector {
  private readonly window: HeartbeatWindow;
  private readonly minStdDev: number;
  private readonly acceptablePause: number;
  private readonly warmup: number;
  private readonly now: () => number;
  private lastHeartbeatAt: number | null = null;

  constructor(options: PhiDetectorOptions = {}) {
    this.window = new HeartbeatWindow(options.windowSize ?? 1000);
    this.minStdDev = options.minStdDeviationMs ?? 10;
    this.acceptablePause = options.acceptablePauseMs ?? 0;
    this.warmup = options.warmupSamples ?? 10;
    this.now = options.now ?? Date.now;
  }

  /** 记录一次心跳到达 */
  heartbeat(atMs?: number): void {
    const at = atMs ?? this.now();
    if (this.lastHeartbeatAt !== null) {
      this.window.add(at - this.lastHeartbeatAt);
    }
    this.lastHeartbeatAt = at;
  }

  /** 当前怀疑度 φ；样本不足（预热期）时返回 0 */
  phi(atMs?: number): number {
    if (this.lastHeartbeatAt === null) return 0;
    if (this.window.size < this.warmup) return 0;

    const at = atMs ?? this.now();
    const elapsed = at - this.lastHeartbeatAt - this.acceptablePause;
    if (elapsed <= 0) return 0;

    const mean = this.window.mean;
    const stdDev = Math.max(this.window.stdDev, this.minStdDev);

    // P_later(t) = 1 - F(t)：等待 t 之后心跳才到达的概率
    const pLater = 1 - normalCdf(elapsed, mean, stdDev);
    if (pLater <= 0) return Number.POSITIVE_INFINITY;
    return -Math.log10(pLater);
  }

  /** 是否仍认为节点存活（φ 未超过阈值） */
  isAlive(threshold: number, atMs?: number): boolean {
    return this.phi(atMs) < threshold;
  }

  /** 已收集的样本数（用于观测预热进度） */
  get sampleCount(): number {
    return this.window.size;
  }

  /** 当前估计的间隔分布参数 */
  get distribution(): { mean: number; stdDev: number } {
    return { mean: this.window.mean, stdDev: this.window.stdDev };
  }
}
