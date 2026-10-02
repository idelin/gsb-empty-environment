/**
 * Clock source abstractions.
 *
 * The guardian compares two independent time bases:
 *  - monotonic: steady, never jumps (process.hrtime / performance.now)
 *  - wall:      civil time, can be stepped by NTP, admins, VM snapshots (Date.now)
 *
 * Both are injectable so tests can simulate clock tampering deterministically.
 */

export type ClockFn = () => number;

export interface ClockPair {
  /** Milliseconds, monotonically non-decreasing, unaffected by system-time changes. */
  monotonicNow: ClockFn;
  /** Milliseconds since Unix epoch, subject to NTP steps and manual changes. */
  wallNow: ClockFn;
}

export function systemClocks(): ClockPair {
  return {
    monotonicNow: () => performance.now(),
    wallNow: () => Date.now(),
  };
}

/** A controllable fake clock pair for tests and demos. */
export class FakeClocks implements ClockPair {
  monoMs: number;
  wallMs: number;

  constructor(startWallMs = 1_700_000_000_000) {
    this.monoMs = 0;
    this.wallMs = startWallMs;
  }

  monotonicNow: ClockFn = () => this.monoMs;
  wallNow: ClockFn = () => this.wallMs;

  /** Advance both clocks together, as healthy time does. */
  advance(ms: number): void {
    this.monoMs += ms;
    this.wallMs += ms;
  }

  /** Step only the wall clock (NTP correction / admin change / VM restore). */
  stepWall(ms: number): void {
    this.wallMs += ms;
  }

  /** Advance only the monotonic clock (wall clock stalled / VM suspend). */
  advanceMonotonicOnly(ms: number): void {
    this.monoMs += ms;
  }
}
