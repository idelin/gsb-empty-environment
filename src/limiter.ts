/**
 * Token-bucket rate limiter with built-in observability.
 *
 * Every admission decision is recorded into `metrics` synchronously, so the
 * conservation invariant (allowed + dropped === requests) holds at any
 * instant, including while a stress test is sampling the metrics endpoint.
 */

import { RateLimiterMetrics } from "./metrics.ts";
import type { MetricsOptions, MetricsSnapshot } from "./metrics.ts";

export interface LimiterOptions extends MetricsOptions {
  /** Maximum burst size (bucket capacity), in tokens. */
  capacity: number;
  /** Sustained refill rate, in tokens per second. */
  refillPerSec: number;
  /** Initial token level. Defaults to full capacity. */
  initialTokens?: number;
}

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

export class TokenBucketLimiter {
  readonly metrics: RateLimiterMetrics;

  private readonly capacity: number;
  private readonly refillPerMs: number;
  private tokens: number;
  private lastRefillMs: number;
  private readonly nowMs: () => number;

  constructor(options: LimiterOptions) {
    if (!(options.capacity > 0)) throw new RangeError("capacity must be > 0");
    if (!(options.refillPerSec > 0)) throw new RangeError("refillPerSec must be > 0");
    this.capacity = options.capacity;
    this.refillPerMs = options.refillPerSec / 1000;
    this.tokens = options.initialTokens ?? options.capacity;
    this.nowMs = options.nowMs ?? Date.now;
    this.lastRefillMs = this.nowMs();
    this.metrics = new RateLimiterMetrics(options);
  }

  private refill(): void {
    const now = this.nowMs();
    if (now > this.lastRefillMs) {
      this.tokens = Math.min(
        this.capacity,
        this.tokens + (now - this.lastRefillMs) * this.refillPerMs,
      );
      this.lastRefillMs = now;
    }
  }

  /** Take a token without recording metrics. */
  private tryTake(): boolean {
    this.refill();
    if (this.tokens >= 1) {
      this.tokens -= 1;
      return true;
    }
    return false;
  }

  /**
   * Non-blocking admission check. Records exactly one of
   * allowed / dropped in the metrics.
   */
  tryAcquire(): boolean {
    if (this.tryTake()) {
      this.metrics.recordAllowed();
      return true;
    }
    this.metrics.recordDropped();
    return false;
  }

  /**
   * Blocking admission: waits until a token is available, then records one
   * allowed request plus the observed wait duration (nanosecond precision).
   */
  async acquire(): Promise<void> {
    const start = performance.now();
    for (;;) {
      if (this.tryTake()) {
        this.metrics.recordAllowed();
        this.metrics.recordWait((performance.now() - start) * 1e6);
        return;
      }
      // Sleep until at least one token can have refilled.
      const deficitMs = (1 - this.tokens) / this.refillPerMs;
      await sleep(Math.max(1, Math.ceil(deficitMs)));
    }
  }

  /** Current token level (gauge). */
  get tokenLevel(): number {
    this.refill();
    return this.tokens;
  }

  /** Standard metrics snapshot with the live token level filled in. */
  snapshot(): MetricsSnapshot {
    return this.metrics.snapshot(this.tokenLevel);
  }
}
