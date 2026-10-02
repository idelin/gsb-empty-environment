/**
 * Core metric primitives.
 *
 * Design goals:
 *  - O(1) recording, sub-microsecond hot path (plain number increments, no
 *    allocation, no formatting on the record path).
 *  - Single-threaded JS => increments are atomic; no locks needed.
 *  - Snapshots are plain immutable objects, safe to read while recording.
 */

export interface Clock {
  /** Monotonic time in milliseconds (fractional). */
  nowMs(): number;
}

export const systemClock: Clock = {
  nowMs: () => performance.now(),
};

/** Monotonic counter (only goes up). */
export class Counter {
  #value = 0;

  inc(delta = 1): void {
    this.#value += delta;
  }

  get value(): number {
    return this.#value;
  }
}

/** Instantaneous value. */
export class Gauge {
  #value = 0;

  set(value: number): void {
    this.#value = value;
  }

  get value(): number {
    return this.#value;
  }
}

export interface HistogramSnapshot {
  buckets: ReadonlyArray<{ le: number; count: number }>;
  count: number;
  sum: number;
}

/**
 * Fixed-bucket histogram (Prometheus-compatible, cumulative buckets).
 * Bucket boundaries are sorted ascending; `observe` is a short linear scan
 * over a preallocated typed array — no allocation on the hot path.
 */
export class Histogram {
  readonly bounds: ReadonlyArray<number>;
  readonly #counts: Float64Array; // cumulative counts per bound
  #count = 0;
  #sum = 0;

  constructor(bounds: ReadonlyArray<number>) {
    if (bounds.length === 0) throw new Error("histogram needs >= 1 bucket");
    const sorted = [...bounds].sort((a, b) => a - b);
    for (let i = 1; i < sorted.length; i++) {
      if (sorted[i] === sorted[i - 1]) {
        throw new Error("duplicate histogram bucket bound");
      }
    }
    this.bounds = sorted;
    this.#counts = new Float64Array(sorted.length);
  }

  observe(value: number): void {
    const bounds = this.bounds;
    const counts = this.#counts;
    // Cumulative semantics: increment every bucket whose bound >= value.
    // Bounds are tiny (<= 16), linear scan from the first matching bucket.
    let i = 0;
    while (i < bounds.length && bounds[i] < value) i++;
    for (; i < bounds.length; i++) counts[i]++;
    this.#count++;
    this.#sum += value;
  }

  get count(): number {
    return this.#count;
  }

  get sum(): number {
    return this.#sum;
  }

  snapshot(): HistogramSnapshot {
    const buckets = new Array<{ le: number; count: number }>(this.bounds.length);
    for (let i = 0; i < this.bounds.length; i++) {
      buckets[i] = { le: this.bounds[i], count: this.#counts[i] };
    }
    return { buckets, count: this.#count, sum: this.#sum };
  }
}

export interface WindowSnapshot {
  windowStartMs: number;
  windowEndMs: number;
  allowed: number;
  dropped: number;
}

export interface MetricsSnapshot {
  /** Cumulative since process start. */
  allowedTotal: number;
  droppedTotal: number;
  /** Current (just-closed) window counters. */
  window: WindowSnapshot;
  waitDuration: HistogramSnapshot;
  tokenLevel: number;
  /** Derived: allowed per second over the closed window. */
  windowThroughputPerSec: number;
}

/**
 * The full metric set for one rate limiter.
 *
 * Windowing: `snapshot()` atomically closes the current window (reads +
 * resets the window counters) and returns an immutable snapshot. Recording
 * never blocks snapshotting and vice versa (single-threaded, no locks).
 */
export class RateLimiterMetrics {
  readonly #clock: Clock;

  readonly #allowedTotal = new Counter();
  readonly #droppedTotal = new Counter();
  readonly #waitDuration: Histogram;
  readonly #tokenLevel = new Gauge();

  #windowAllowed = 0;
  #windowDropped = 0;
  #windowStartMs: number;

  constructor(
    clock: Clock = systemClock,
    waitBuckets: ReadonlyArray<number> = RateLimiterMetrics.defaultWaitBucketsSec,
  ) {
    this.#clock = clock;
    this.#waitDuration = new Histogram(waitBuckets);
    this.#windowStartMs = clock.nowMs();
  }

  /** Wait-duration buckets in seconds, sub-ms up to 5s. */
  static readonly defaultWaitBucketsSec: ReadonlyArray<number> = [
    0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5,
  ];

  /** Hot path: a request was admitted. */
  recordAllowed(tokenLevel: number): void {
    this.#allowedTotal.inc();
    this.#windowAllowed++;
    this.#tokenLevel.set(tokenLevel);
  }

  /** Hot path: a request was dropped (limiter refused). */
  recordDropped(tokenLevel: number): void {
    this.#droppedTotal.inc();
    this.#windowDropped++;
    this.#tokenLevel.set(tokenLevel);
  }

  /** Hot path: record how long an admitted request waited for a token. */
  recordWaitDuration(seconds: number): void {
    this.#waitDuration.observe(seconds);
  }

  /** Hot path: refresh the token-level gauge. */
  recordTokenLevel(level: number): void {
    this.#tokenLevel.set(level);
  }

  /** Current token level without disturbing anything. */
  get tokenLevel(): number {
    return this.#tokenLevel.value;
  }

  get allowedTotal(): number {
    return this.#allowedTotal.value;
  }

  get droppedTotal(): number {
    return this.#droppedTotal.value;
  }

  get waitDuration(): Histogram {
    return this.#waitDuration;
  }

  /**
   * Close the current window and return a consistent snapshot.
   * Window counters are rotated atomically (no event-loop yield between
   * read and reset), so no event is ever lost or double counted:
   * for every snapshot sequence, sum(window.allowed) === allowedTotal.
   */
  snapshot(): MetricsSnapshot {
    const now = this.#clock.nowMs();
    const window: WindowSnapshot = {
      windowStartMs: this.#windowStartMs,
      windowEndMs: now,
      allowed: this.#windowAllowed,
      dropped: this.#windowDropped,
    };
    this.#windowAllowed = 0;
    this.#windowDropped = 0;
    this.#windowStartMs = now;

    const windowSec = Math.max((window.windowEndMs - window.windowStartMs) / 1000, 1e-9);
    return {
      allowedTotal: this.#allowedTotal.value,
      droppedTotal: this.#droppedTotal.value,
      window,
      waitDuration: this.#waitDuration.snapshot(),
      tokenLevel: this.#tokenLevel.value,
      windowThroughputPerSec: window.allowed / windowSec,
    };
  }
}
