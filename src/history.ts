export class BoundedHistory {
  private samples: number[] = [];
  private sum = 0;
  private sumSq = 0;
  readonly capacity: number;

  constructor(capacity: number) {
    if (capacity < 2) throw new Error("capacity must be >= 2");
    this.capacity = capacity;
  }

  get count(): number {
    return this.samples.length;
  }

  add(value: number): void {
    if (this.samples.length === this.capacity) {
      const evicted = this.samples.shift() as number;
      this.sum -= evicted;
      this.sumSq -= evicted * evicted;
    }
    this.samples.push(value);
    this.sum += value;
    this.sumSq += value * value;
  }

  get mean(): number {
    return this.samples.length === 0 ? 0 : this.sum / this.samples.length;
  }

  get stddev(): number {
    const n = this.samples.length;
    if (n < 2) return 0;
    const variance = Math.max(0, this.sumSq / n - this.mean * this.mean);
    return Math.sqrt(variance);
  }

  quantile(q: number): number {
    const n = this.samples.length;
    if (n === 0) return 0;
    const sorted = [...this.samples].sort((a, b) => a - b);
    const idx = Math.min(n - 1, Math.max(0, Math.ceil(q * n) - 1));
    return sorted[idx];
  }
}
