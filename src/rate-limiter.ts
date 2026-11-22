import type { Clock } from './clock.ts';
import type { Evidence } from './types.ts';

/**
 * 滑动窗口日志限速器：记录每个键在窗口内的尝试时间戳。
 * 同一个实例可分别用于「来源」与「账号」两个维度。
 */
export class SlidingWindowRateLimiter {
  private readonly hits = new Map<string, number[]>();

  constructor(
    private readonly clock: Clock,
    private readonly windowMs: number,
    private readonly maxAttempts: number,
  ) {}

  /** 清理过期记录并统计当前窗口内的尝试次数（不含本次）。 */
  private countWithinWindow(key: string, now: number): number[] {
    const timestamps = this.hits.get(key);
    if (!timestamps) {
      return [];
    }
    const fresh = timestamps.filter((ts) => now - ts < this.windowMs);
    if (fresh.length === 0) {
      this.hits.delete(key);
    } else {
      this.hits.set(key, fresh);
    }
    return fresh;
  }

  /**
   * 登记一次尝试。
   * @returns allowed=false 表示该维度已超限；evidence 附带可观测计数。
   */
  checkAndRecord(key: string): { allowed: boolean; evidence: Evidence } {
    const now = this.clock.now();
    const fresh = this.countWithinWindow(key, now);
    const priorCount = fresh.length;
    const allowed = priorCount < this.maxAttempts;
    fresh.push(now);
    this.hits.set(key, fresh);
    return {
      allowed,
      evidence: {
        key,
        windowMs: this.windowMs,
        maxAttempts: this.maxAttempts,
        attemptsInWindow: fresh.length,
        priorAttempts: priorCount,
      },
    };
  }

  /** 某键当前窗口内的尝试次数（只读，不移除）。 */
  currentCount(key: string): number {
    const now = this.clock.now();
    return this.countWithinWindow(key, now).length;
  }

  /** 某键最早一条窗口记录距今的毫秒数；无记录返回 null。 */
  oldestAgeMs(key: string): number | null {
    const now = this.clock.now();
    const fresh = this.countWithinWindow(key, now);
    if (fresh.length === 0) {
      return null;
    }
    return now - fresh[0];
  }
}
