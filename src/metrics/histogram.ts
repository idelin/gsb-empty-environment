/**
 * Exponential-bucket latency histogram in nanoseconds.
 * Bucket i covers (2^(i-1), 2^i] ns; bucket 0 catches <= 1ns and an
 * overflow slot catches anything above the largest bound.
 */
export const DEFAULT_BUCKET_COUNT = 64;

export interface HistogramSnapshot {
  count: number;
  sumNs: number;
  minNs: number;
  maxNs: number;
  meanNs: number;
  buckets: { leNs: number; count: number }[];
  percentiles: { p50: number; p90: number; p95: number; p99: number };
}

export class LatencyHistogram {
  private readonly buckets: number[];
  private count = 0;
  private sum = 0;
  private min = Number.POSITIVE_INFINITY;
  private max = 0;

  constructor(bucketCount: number = DEFAULT_BUCKET_COUNT) {
    this.buckets = new Array<number>(bucketCount + 1).fill(0);
  }

  record(ns: number): void {
    const v = ns < 0 ? 0 : ns;
    this.count++;
    this.sum += v;
    if (v < this.min) this.min = v;
    if (v > this.max) this.max = v;
    let idx: number;
    if (v <= 1) {
      idx = 0;
    } else {
      idx = Math.min(Math.ceil(Math.log2(v)) + 1, this.buckets.length - 1);
    }
    this.buckets[idx]++;
  }

  get total(): number {
    return this.count;
  }

  private percentile(p: number): number {
    if (this.count === 0) return 0;
    const rank = Math.max(1, Math.ceil((p / 100) * this.count));
    let cumulative = 0;
    for (let i = 0; i < this.buckets.length; i++) {
      cumulative += this.buckets[i];
      if (cumulative >= rank) {
        const upper = i === this.buckets.length - 1 ? Math.max(this.max, 2 ** (i - 1)) : 2 ** (i - 1);
        const lower = i <= 1 ? 0 : 2 ** (i - 2);
        return Math.min(upper, Math.max(lower, Math.min(upper, this.max)));
      }
    }
    return this.max;
  }

  snapshot(): HistogramSnapshot {
    const buckets: { leNs: number; count: number }[] = [];
    for (let i = 0; i < this.buckets.length - 1; i++) {
      if (this.buckets[i] > 0) {
        buckets.push({ leNs: i === 0 ? 1 : 2 ** (i - 1), count: this.buckets[i] });
      }
    }
    if (this.buckets[this.buckets.length - 1] > 0) {
      buckets.push({ leNs: Number.POSITIVE_INFINITY, count: this.buckets[this.buckets.length - 1] });
    }
    return {
      count: this.count,
      sumNs: this.sum,
      minNs: this.count === 0 ? 0 : this.min,
      maxNs: this.count === 0 ? 0 : this.max,
      meanNs: this.count === 0 ? 0 : this.sum / this.count,
      buckets,
      percentiles: {
        p50: this.percentile(50),
        p90: this.percentile(90),
        p95: this.percentile(95),
        p99: this.percentile(99),
      },
    };
  }

  reset(): void {
    this.buckets.fill(0);
    this.count = 0;
    this.sum = 0;
    this.min = Number.POSITIVE_INFINITY;
    this.max = 0;
  }
}
