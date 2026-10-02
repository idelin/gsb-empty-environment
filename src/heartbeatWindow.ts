/**
 * 心跳到达间隔的滑动窗口采样器。
 * 保存最近 N 次心跳间隔，用于估计网络延迟分布（均值 / 方差）。
 */
export class HeartbeatWindow {
  private readonly maxSize: number;
  private samples: number[] = [];
  private sum = 0;
  private sumSquares = 0;

  constructor(maxSize = 1000) {
    if (maxSize < 2) throw new Error("maxSize 必须 >= 2");
    this.maxSize = maxSize;
  }

  add(intervalMs: number): void {
    if (intervalMs <= 0) return; // 忽略乱序/重复到达
    this.samples.push(intervalMs);
    this.sum += intervalMs;
    this.sumSquares += intervalMs * intervalMs;
    if (this.samples.length > this.maxSize) {
      const evicted = this.samples.shift() as number;
      this.sum -= evicted;
      this.sumSquares -= evicted * evicted;
    }
  }

  get size(): number {
    return this.samples.length;
  }

  get mean(): number {
    return this.samples.length === 0 ? 0 : this.sum / this.samples.length;
  }

  get variance(): number {
    const n = this.samples.length;
    if (n < 2) return 0;
    // E[x^2] - E[x]^2，浮点误差下限截断为 0
    return Math.max(0, this.sumSquares / n - this.mean * this.mean);
  }

  get stdDev(): number {
    return Math.sqrt(this.variance);
  }
}
