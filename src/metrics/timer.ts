import { EngineError, ErrorCodes } from "../errors.ts";
import type { Histogram } from "./primitives.ts";

/**
 * High-resolution timer backed by a histogram (milliseconds, float).
 * Uses performance.now() which has sub-microsecond resolution.
 */
export class Timer {
  private readonly histogram: Histogram;

  constructor(histogram: Histogram) {
    this.histogram = histogram;
  }

  /** Starts a measurement; call the returned closure exactly once to stop. */
  start(): () => number {
    const startedAt = performance.now();
    let stopped = false;
    return () => {
      if (stopped) {
        throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, "timer stop closure invoked twice");
      }
      stopped = true;
      const elapsed = Math.max(0, performance.now() - startedAt);
      this.histogram.observe(elapsed);
      return elapsed;
    };
  }

  /** Times a synchronous function, recording even when it throws. */
  time<T>(fn: () => T): T {
    const stop = this.start();
    try {
      return fn();
    } finally {
      stop();
    }
  }

  /** Times an async function, recording even when it rejects. */
  async timeAsync<T>(fn: () => Promise<T>): Promise<T> {
    const stop = this.start();
    try {
      return await fn();
    } finally {
      stop();
    }
  }

  observeDuration(ms: number): void {
    this.histogram.observe(ms);
  }

  get histogram_(): Histogram {
    return this.histogram;
  }
}
