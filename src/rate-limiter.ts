/**
 * Token-bucket rate limiter with built-in observability.
 * Every admission decision is recorded into a RateLimiterMetrics instance.
 */
import { RateLimiterMetrics, systemClock } from "./metrics.ts";
import type { Clock } from "./metrics.ts";

export interface TokenBucketOptions {
  /** Max burst size (bucket capacity, in tokens). */
  capacity: number;
  /** Sustained refill rate (tokens per second). */
  refillPerSec: number;
  clock?: Clock;
  metrics?: RateLimiterMetrics;
}

export class TokenBucketRateLimiter {
  readonly #capacity: number;
  readonly #refillPerSec: number;
  readonly #clock: Clock;
  readonly metrics: RateLimiterMetrics;

  #tokens: number;
  #lastRefillMs: number;

  constructor(options: TokenBucketOptions) {
    if (options.capacity <= 0) throw new Error("capacity must be > 0");
    if (options.refillPerSec <= 0) throw new Error("refillPerSec must be > 0");
    this.#capacity = options.capacity;
    this.#refillPerSec = options.refillPerSec;
    this.#clock = options.clock ?? systemClock;
    this.metrics = options.metrics ?? new RateLimiterMetrics(this.#clock);
    this.#tokens = options.capacity;
    this.#lastRefillMs = this.#clock.nowMs();
  }

  #refill(): void {
    const now = this.#clock.nowMs();
    const elapsedSec = (now - this.#lastRefillMs) / 1000;
    if (elapsedSec > 0) {
      this.#tokens = Math.min(this.#capacity, this.#tokens + elapsedSec * this.#refillPerSec);
      this.#lastRefillMs = now;
    }
  }

  /** Non-blocking: admit if a token is available, otherwise drop. */
  tryAcquire(cost = 1): boolean {
    this.#refill();
    if (this.#tokens >= cost) {
      this.#tokens -= cost;
      this.metrics.recordAllowed(this.#tokens);
      return true;
    }
    this.metrics.recordDropped(this.#tokens);
    return false;
  }

  /**
   * Blocking: wait until a token becomes available (with optional timeout).
   * Records the wait duration in the histogram. Returns false on timeout
   * (counted as a drop).
   */
  async acquire(cost = 1, timeoutMs = Infinity): Promise<boolean> {
    const startMs = this.#clock.nowMs();
    for (;;) {
      this.#refill();
      if (this.#tokens >= cost) {
        this.#tokens -= cost;
        const waitedSec = (this.#clock.nowMs() - startMs) / 1000;
        this.metrics.recordWaitDuration(waitedSec);
        this.metrics.recordAllowed(this.#tokens);
        return true;
      }
      const deficit = cost - this.#tokens;
      const waitMs = (deficit / this.#refillPerSec) * 1000;
      if (this.#clock.nowMs() + waitMs - startMs > timeoutMs) {
        this.metrics.recordDropped(this.#tokens);
        return false;
      }
      await sleep(Math.min(waitMs, 1000));
    }
  }

  get currentTokens(): number {
    this.#refill();
    return this.#tokens;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
