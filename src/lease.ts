import type { Clock } from "./clock.ts";

/**
 * 租约剩余时间低于该阈值时，读路径自动降级为全量网络心跳确认。
 * 规范要求：100ms。
 */
export const SAFE_READ_THRESHOLD_MS = 100;

export interface LeaseManagerOptions {
  clock: Clock;
  /** 各节点时钟最大偏移上界 ε（毫秒）。安全窗口必须扣除该上界。 */
  maxClockSkewMs: number;
  /** 触发降级的剩余租约阈值，默认 100ms。 */
  safeReadThresholdMs?: number;
}

/**
 * 主节点侧租约管理器。
 *
 * 线性一致性论证：
 *  - 从节点承诺在 promisedUntil（其本地时钟）之前不选举新主；
 *  - 主节点本地租约到期时刻为 expiry；
 *  - 只要 当前主节点本地时间 + 最大时钟偏移 ε < expiry，
 *    则任意从节点的本地时间都尚未到达 promisedUntil，
 *    集群中不可能存在其他已当选的主节点，本地副本即最新已提交数据，
 *    本地读满足严格线性一致性 —— 该区间称为“绝对安全窗口”。
 */
export class LeaseManager {
  private clock: Clock;
  private maxClockSkewMs: number;
  private safeReadThresholdMs: number;
  private leaseExpiry = -Infinity;

  constructor(options: LeaseManagerOptions) {
    this.clock = options.clock;
    this.maxClockSkewMs = options.maxClockSkewMs;
    this.safeReadThresholdMs = options.safeReadThresholdMs ?? SAFE_READ_THRESHOLD_MS;
  }

  /** 授予/续期租约：从当前时刻起 validMs 毫秒内有效。 */
  grant(validMs: number): void {
    this.leaseExpiry = this.clock.now() + validMs;
  }

  revoke(): void {
    this.leaseExpiry = -Infinity;
  }

  /** 租约剩余时间（毫秒），可能为负（已过期）。 */
  remainingMs(): number {
    return this.leaseExpiry - this.clock.now();
  }

  /**
   * 是否处于绝对安全窗口：
   * 扣除最大时钟偏移 ε 后，剩余租约仍 >= 降级阈值（100ms）。
   */
  isInAbsoluteSafeWindow(): boolean {
    return this.remainingMs() - this.maxClockSkewMs >= this.safeReadThresholdMs;
  }

  get maxSkewMs(): number {
    return this.maxClockSkewMs;
  }

  get thresholdMs(): number {
    return this.safeReadThresholdMs;
  }
}
