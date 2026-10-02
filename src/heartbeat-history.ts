export class HeartbeatHistory {
  private readonly maxSampleSize: number;
  private intervals: number[] = [];
  private sum = 0;
  private sumOfSquares = 0;

  constructor(maxSampleSize: number) {
    this.maxSampleSize = maxSampleSize;
  }

  add(intervalMs: number): void {
    if (this.intervals.length >= this.maxSampleSize) {
      const dropped = this.intervals.shift() as number;
      this.sum -= dropped;
      this.sumOfSquares -= dropped * dropped;
    }
    this.intervals.push(intervalMs);
    this.sum += intervalMs;
    this.sumOfSquares += intervalMs * intervalMs;
  }

  get size(): number {
    return this.intervals.length;
  }

  get mean(): number {
    return this.sum / this.intervals.length;
  }

  get variance(): number {
    const mean = this.mean;
    return Math.max(this.sumOfSquares / this.intervals.length - mean * mean, 0);
  }

  get stdDeviation(): number {
    return Math.sqrt(this.variance);
  }
}
