import type { Clock } from "./clock.ts";

export interface LeaseOptions {
  /** 租约总时长（从节点承诺不接纳新主的时间窗口） */
  ttlMs: number;
  /** 绝对安全窗口下界：剩余租约不足该值时必须降级为全量网络心跳确认（验收标准：100ms） */
  safeWindowMs: number;
  /** 时钟漂移上界 ε：主节点按最悲观情况收缩本地租约有效期 */
  clockSkewBoundMs: number;
}

/**
 * 租约管理器。
 *
 * 线性一致性依据：从节点在 [grantTime, grantTime + ttl] 内承诺拒绝任何新主当选，
 * 因此主节点在本地时钟 now < grantTime + ttl - ε 期间，集群中不可能存在更新的主，
 * 本地已应用全部已提交写，直接本地读即满足严格线性一致。
 */
export class LeaseManager {
  private readonly ttlMs: number;
  private readonly safeWindowMs: number;
  private readonly skewMs: number;
  private readonly clock: Clock;
  private expiresAt = Number.NEGATIVE_INFINITY;
  private term = 0;

  constructor(options: LeaseOptions, clock: Clock) {
    this.ttlMs = options.ttlMs;
    this.safeWindowMs = options.safeWindowMs;
    this.skewMs = options.clockSkewBoundMs;
    this.clock = clock;
  }

  /** 心跳法定多数确认成功后授予/续期租约，本地有效期按 ε 收缩。 */
  grant(): void {
    this.term += 1;
    this.expiresAt = this.clock.now() + this.ttlMs - this.skewMs;
  }

  /** 当前租约剩余有效毫秒数（已扣除时钟漂移）。 */
  remainingMs(): number {
    return this.expiresAt - this.clock.now();
  }

  /** 租约是否仍处于绝对安全窗口：剩余时间 > safeWindowMs，可零网络本地读。 */
  isInAbsoluteSafeWindow(): boolean {
    return this.remainingMs() > this.safeWindowMs;
  }

  /** 租约是否仍有效（但可能已离开安全窗口，需要降级确认）。 */
  isValid(): boolean {
    return this.remainingMs() > 0;
  }

  currentTerm(): number {
    return this.term;
  }
}
