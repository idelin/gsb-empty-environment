/**
 * Sliding-window histogram with hybrid fixed-width + logarithmic buckets.
 *
 * Layout (defaults):
 *   - Fixed region: [0, 128ms) split into 512 buckets of 0.25ms.
 *     Representative = bucket midpoint, max absolute error = 0.125ms.
 *   - Log region: [128ms, 2^27 ms) with 128 buckets per octave
 *     (ratio r = 2^(1/128)). Representative = geometric mean of bucket
 *     bounds, max relative error = 2^(1/256) - 1 ~= 0.2713%.
 *
 * Only one bucket index per in-window sample is retained (ring buffer),
 * never the raw samples. Eviction performs reverse compensation by
 * decrementing the evicted bucket's count. Memory: O(capacity + buckets).
 */

export interface HistogramOptions {
  /** Sliding window capacity in samples. Default 1000. */
  capacity?: number;
  /** Width of each fixed bucket in ms. Default 0.25. */
  fixedWidthMs?: number;
  /** Number of fixed-width buckets. Default 512 (covers [0, 128ms)). */
  fixedCount?: number;
  /** Log buckets per octave in the log region. Default 128. */
  logBucketsPerOctave?: number;
  /** Largest value (ms) with a dedicated bucket; larger values clamp. Default 2^27. */
  maxValueMs?: number;
}

export class SlidingWindowHistogram {
  readonly capacity: number;
  readonly fixedWidthMs: number;
  readonly fixedCount: number;
  readonly logBucketsPerOctave: number;
  readonly fixedMaxMs: number;
  readonly bucketCount: number;

  private readonly counts: Uint32Array;
  private readonly ring: Int32Array;
  private head = 0;
  private size = 0;
  private readonly logRatio: number; // ln(r), r = 2^(1/k)

  constructor(options: HistogramOptions = {}) {
    this.capacity = options.capacity !== undefined ? options.capacity : 1000;
    this.fixedWidthMs = options.fixedWidthMs !== undefined ? options.fixedWidthMs : 0.25;
    this.fixedCount = options.fixedCount !== undefined ? options.fixedCount : 512;
    this.logBucketsPerOctave =
      options.logBucketsPerOctave !== undefined ? options.logBucketsPerOctave : 128;
    const maxValueMs = options.maxValueMs !== undefined ? options.maxValueMs : Math.pow(2, 27);

    if (this.capacity <= 0 || this.fixedWidthMs <= 0 || this.fixedCount <= 0) {
      throw new Error('capacity, fixedWidthMs and fixedCount must be positive');
    }
    this.fixedMaxMs = this.fixedWidthMs * this.fixedCount;
    if (maxValueMs <= this.fixedMaxMs) {
      throw new Error('maxValueMs must exceed the fixed region');
    }

    this.logRatio = Math.LN2 / this.logBucketsPerOctave;
    const logCount =
      Math.ceil(Math.log(maxValueMs / this.fixedMaxMs) / this.logRatio) + 1;
    this.bucketCount = this.fixedCount + logCount;
    this.counts = new Uint32Array(this.bucketCount);
    this.ring = new Int32Array(this.capacity);
  }

  /** Number of samples currently inside the window. */
  get length(): number {
    return this.size;
  }

  /** Total samples seen so far in the window (same as length). */
  get total(): number {
    return this.size;
  }

  /** Theoretical max relative error of quantile estimates. */
  get maxRelativeError(): number {
    return Math.exp(this.logRatio / 2) - 1;
  }

  bucketIndex(valueMs: number): number {
    let v = valueMs;
    if (!isFinite(v)) {
      return this.bucketCount - 1;
    }
    if (v < 0) {
      v = 0;
    }
    if (v < this.fixedMaxMs) {
      return Math.min(this.fixedCount - 1, Math.floor(v / this.fixedWidthMs));
    }
    const j = Math.floor(Math.log(v / this.fixedMaxMs) / this.logRatio);
    return Math.min(this.bucketCount - 1, this.fixedCount + j);
  }

  /** Representative value of a bucket (midpoint / geometric mean). */
  bucketValue(index: number): number {
    if (index < this.fixedCount) {
      return (index + 0.5) * this.fixedWidthMs;
    }
    const j = index - this.fixedCount;
    return this.fixedMaxMs * Math.exp((j + 0.5) * this.logRatio);
  }

  /** Add a sample; evicts the oldest one (reverse compensation) when full. */
  record(valueMs: number): void {
    const idx = this.bucketIndex(valueMs);
    if (this.size === this.capacity) {
      const evicted = this.ring[this.head];
      this.counts[evicted]--;
    } else {
      this.size++;
    }
    this.ring[this.head] = idx;
    this.counts[idx]++;
    this.head = (this.head + 1) % this.capacity;
  }

  /** Nearest-rank quantile, q in (0, 1]. NaN when the window is empty. */
  quantile(q: number): number {
    if (this.size === 0) {
      return NaN;
    }
    const rank = Math.max(1, Math.ceil(q * this.size));
    let acc = 0;
    for (let i = 0; i < this.bucketCount; i++) {
      acc += this.counts[i];
      if (acc >= rank) {
        return this.bucketValue(i);
      }
    }
    return this.bucketValue(this.bucketCount - 1);
  }

  p90(): number {
    return this.quantile(0.9);
  }

  p99(): number {
    return this.quantile(0.99);
  }

  /** Copy of per-bucket counts (mainly for tests and introspection). */
  snapshotCounts(): number[] {
    return Array.prototype.slice.call(this.counts);
  }
}
