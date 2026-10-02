import type { Clock } from "../src/metrics.ts";

/** Deterministic, manually advanced clock for tests. */
export class FakeClock implements Clock {
  now = 1_000_000; // ms
  nowMs(): number {
    return this.now;
  }
  advance(ms: number): void {
    this.now += ms;
  }
}
