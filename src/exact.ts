/**
 * Reference implementation that retains every in-window sample and computes
 * exact nearest-rank quantiles by sorting. Used by tests and the demo to
 * measure the histogram's approximation error.
 */
export class ExactWindow {
  private readonly ring: number[];
  private head = 0;
  private size = 0;
  readonly capacity: number;

  constructor(capacity: number) {
    this.capacity = capacity;
    this.ring = new Array<number>(capacity);
  }

  get length(): number {
    return this.size;
  }

  record(valueMs: number): void {
    this.ring[this.head] = valueMs;
    this.head = (this.head + 1) % this.capacity;
    if (this.size < this.capacity) {
      this.size++;
    }
  }

  quantile(q: number): number {
    if (this.size === 0) {
      return NaN;
    }
    const sorted = this.ring.slice(0, this.size).sort((a, b) => a - b);
    const rank = Math.max(1, Math.ceil(q * this.size));
    return sorted[rank - 1];
  }

  p90(): number {
    return this.quantile(0.9);
  }

  p99(): number {
    return this.quantile(0.99);
  }
}
