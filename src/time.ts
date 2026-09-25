/**
 * Exact time representation.
 *
 * All times inside the engine are `Ticks`: integer microseconds held in a
 * `number` (exact up to 2^53 ticks ≈ 285 years). Because every duration,
 * delay, offset, keyframe and event time is an integer, period boundaries,
 * interval containment and event deduplication are exact — no float drift,
 * no double-fired boundary events.
 */
export type Ticks = number;

export const TICKS_PER_MS = 1_000;
export const TICKS_PER_SEC = 1_000_000;

/** Convert milliseconds to exact ticks (rounds to nearest tick). */
export function ticks(ms: number): Ticks {
  return Math.round(ms * TICKS_PER_MS);
}

/** Convert seconds to exact ticks (rounds to nearest tick). */
export function ticksOfSec(seconds: number): Ticks {
  return Math.round(seconds * TICKS_PER_SEC);
}

/** Convert ticks back to milliseconds (for display only). */
export function msOf(t: Ticks): number {
  return t / TICKS_PER_MS;
}

export function assertTicks(t: Ticks, what: string): void {
  if (!Number.isInteger(t)) {
    throw new Error(`${what} must be an integer number of ticks, got ${t}`);
  }
}
