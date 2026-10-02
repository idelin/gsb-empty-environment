/**
 * Rate limiter runtime metrics.
 *
 * Design goals:
 *  - O(1) hot path: recording a sample is a couple of counter increments plus
 *    one Date.now() call, costing well under a microsecond.
 *  - Data conservation: allowedTotal + droppedTotal === requestsTotal at all
 *    times, both cumulatively and per time window.
 *  - Tumbling time windows kept in a fixed-size ring buffer, rotated lazily
 *    on the recording thread (no timers, no background work).
 */

export interface HistogramBucket {
  /** Upper bound in seconds (Prometheus `le` label). */
  le: number;
  /** Cumulative count of observations <= le. */
  count: number;
}

export interface HistogramSnapshot {
  buckets: HistogramBucket[];
  count: number;
  /** Sum of all observed durations, in nanoseconds. */
  sumNanos: number;
  minNanos: number;
  maxNanos: number;
}

export interface WindowSnapshot {
  /** Window start, epoch milliseconds. */
  startMs: number;
  /** Window length in milliseconds. */
  windowMs: number;
  allowed: number;
  dropped: number;
  /** True while this window is still collecting samples. */
  current: boolean;
}

export interface MetricsSnapshot {
  timestampMs: number;
  windowMs: number;
  allowedTotal: number;
  droppedTotal: number;
  /** allowedTotal + droppedTotal (conservation invariant). */
  requestsTotal: number;
  /** Allowed requests per second over the last completed window. */
  throughputPerSec: number;
  /** Dropped requests in the last completed window. */
  droppedLastWindow: number;
  /** Current token bucket level, filled in by the limiter at snapshot time. */
  currentTokenLevel: number;
  waitDuration: HistogramSnapshot;
  /** Recent tumbling windows, oldest first, current window last. */
  windows: WindowSnapshot[];
}

export interface MetricsOptions {
  /** Tumbling window length in milliseconds. Default 1000. */
  windowMs?: number;
  /** Number of windows retained in the ring. Default 60. */
  windowCount?: number;
  /** Histogram bucket upper bounds, in seconds. Default: 100us .. 1s. */
  bucketBoundsSec?: readonly number[];
  /** Clock override for tests. Must return epoch milliseconds. */
  nowMs?: () => number;
}

export const DEFAULT_BUCKET_BOUNDS_SEC: readonly number[] = [
  0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1,
];

interface Window {
  startMs: number;
  allowed: number;
  dropped: number;
}

export class RateLimiterMetrics {
  private readonly windowMs: number;
  private readonly windowCount: number;
  private readonly boundsNanos: readonly number[];
  private readonly bucketCounts: number[];
  private readonly nowMs: () => number;

  private allowedTotal = 0;
  private droppedTotal = 0;
  private waitCount = 0;
  private waitSumNanos = 0;
  private waitMinNanos = Number.POSITIVE_INFINITY;
  private waitMaxNanos = 0;

  private windows: Window[] = [];
  private lastCompletedWindow: Window | null = null;

  constructor(options: MetricsOptions = {}) {
    this.windowMs = options.windowMs ?? 1000;
    this.windowCount = options.windowCount ?? 60;
    const bounds = options.bucketBoundsSec ?? DEFAULT_BUCKET_BOUNDS_SEC;
    this.boundsNanos = bounds.map((s) => s * 1e6);
    this.bucketCounts = new Array(this.boundsNanos.length).fill(0);
    this.nowMs = options.nowMs ?? Date.now;
    this.windows.push({ startMs: this.currentWindowStart(), allowed: 0, dropped: 0 });
  }

  private currentWindowStart(): number {
    return Math.floor(this.nowMs() / this.windowMs) * this.windowMs;
  }

  /** Lazily advance the window ring. Amortized O(1). */
  private rotate(): void {
    const start = this.currentWindowStart();
    let tail = this.windows[this.windows.length - 1];
    if (start === tail.startMs) return;
    // Fill any skipped (idle) windows so per-window series stay aligned.
    while (tail.startMs < start) {
      const nextStart = tail.startMs + this.windowMs;
      const next: Window = { startMs: nextStart, allowed: 0, dropped: 0 };
      this.lastCompletedWindow = tail;
      this.windows.push(next);
      tail = next;
    }
    while (this.windows.length > this.windowCount) this.windows.shift();
  }

  /** Record one admitted request. Hot path: sub-microsecond. */
  recordAllowed(): void {
    this.allowedTotal++;
    this.rotate();
    this.windows[this.windows.length - 1].allowed++;
  }

  /** Record one rejected (dropped) request. Hot path: sub-microsecond. */
  recordDropped(): void {
    this.droppedTotal++;
    this.rotate();
    this.windows[this.windows.length - 1].dropped++;
  }

  /** Record the wait duration of an admitted request, in nanoseconds. */
  recordWait(nanos: number): void {
    this.waitCount++;
    this.waitSumNanos += nanos;
    if (nanos < this.waitMinNanos) this.waitMinNanos = nanos;
    if (nanos > this.waitMaxNanos) this.waitMaxNanos = nanos;
    // Linear scan is fastest for the small bucket counts used here.
    const bounds = this.boundsNanos;
    for (let i = 0; i < bounds.length; i++) {
      if (nanos <= bounds[i]) {
        this.bucketCounts[i]++;
        return;
      }
    }
  }

  /** Standard metrics snapshot. `tokenLevel` is supplied by the limiter. */
  snapshot(tokenLevel: number): MetricsSnapshot {
    this.rotate();
    const last = this.lastCompletedWindow;
    const windows: WindowSnapshot[] = this.windows.map((w, i) => ({
      startMs: w.startMs,
      windowMs: this.windowMs,
      allowed: w.allowed,
      dropped: w.dropped,
      current: i === this.windows.length - 1,
    }));
    let cumulative = 0;
    const buckets: HistogramBucket[] = this.boundsNanos.map((nanos, i) => {
      cumulative += this.bucketCounts[i];
      return { le: nanos / 1e6, count: cumulative };
    });
    return {
      timestampMs: this.nowMs(),
      windowMs: this.windowMs,
      allowedTotal: this.allowedTotal,
      droppedTotal: this.droppedTotal,
      requestsTotal: this.allowedTotal + this.droppedTotal,
      throughputPerSec: last ? last.allowed / (this.windowMs / 1000) : 0,
      droppedLastWindow: last ? last.dropped : 0,
      currentTokenLevel: tokenLevel,
      waitDuration: {
        buckets,
        count: this.waitCount,
        sumNanos: this.waitSumNanos,
        minNanos: this.waitCount === 0 ? 0 : this.waitMinNanos,
        maxNanos: this.waitMaxNanos,
      },
      windows,
    };
  }
}
