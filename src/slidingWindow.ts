import type { Clock } from "./clock.ts";

/**
 * 分桶滑动窗口计数器：把窗口切成若干子桶，随时间旋转，
 * 以 O(桶数) 的代价近似统计最近 windowMs 内的累计值。
 */
export class SlidingWindowCounter {
  private readonly windowMs: number;
  private readonly bucketCount: number;
  private readonly bucketWidthMs: number;
  private readonly clock: Clock;
  private buckets: number[];
  private headTime: number;

  constructor(windowMs: number, bucketCount: number, clock: Clock) {
    if (windowMs <= 0 || bucketCount <= 0) {
      throw new Error("windowMs and bucketCount must be positive");
    }
    this.windowMs = windowMs;
    this.bucketCount = bucketCount;
    this.bucketWidthMs = Math.ceil(windowMs / bucketCount);
    this.clock = clock;
    this.buckets = new Array<number>(bucketCount).fill(0);
    this.headTime = clock.now();
  }

  private rotate(): void {
    const elapsed = this.clock.now() - this.headTime;
    if (elapsed < this.bucketWidthMs) return;
    const steps = Math.floor(elapsed / this.bucketWidthMs);
    if (steps >= this.bucketCount) {
      this.buckets.fill(0);
    } else {
      for (let i = 0; i < steps; i += 1) {
        this.buckets.shift();
        this.buckets.push(0);
      }
    }
    this.headTime += steps * this.bucketWidthMs;
  }

  add(n: number): void {
    this.rotate();
    this.buckets[this.bucketCount - 1] += n;
  }

  sum(): number {
    this.rotate();
    let total = 0;
    for (const value of this.buckets) total += value;
    return total;
  }
}
