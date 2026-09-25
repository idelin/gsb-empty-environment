import type { Ticks } from './time.ts';

export type Direction = 'normal' | 'reverse' | 'alternate' | 'alternate-reverse';
export type Fill = 'none' | 'forwards' | 'backwards' | 'both';
export type Phase = 'before' | 'active' | 'after';

export interface Timing {
  /** Start delay in ticks. May be negative: the animation then starts partway through. */
  delay?: Ticks;
  /** Iteration duration in ticks. Zero is defined (instant transition). */
  duration: Ticks;
  /** Iteration count, integer >= 1 or Infinity. Defaults to 1. */
  iterations?: number;
  direction?: Direction;
  fill?: Fill;
}

export interface NormalizedTiming {
  delay: Ticks;
  duration: Ticks;
  iterations: number;
  direction: Direction;
  fill: Fill;
}

/**
 * Sentinels used for zero-duration animations. They sit outside any legal
 * keyframe time, so `KeyframeTrack.sample` clamps them to the first/last
 * keyframe value respectively.
 */
export const TRACK_START_SENTINEL: Ticks = -1;
export const TRACK_END_SENTINEL: Ticks = Number.MAX_SAFE_INTEGER;

export function normalizeTiming(t: Timing): NormalizedTiming {
  const delay = t.delay ?? 0;
  const iterations = t.iterations ?? 1;
  if (!Number.isInteger(delay)) throw new Error(`delay must be integer ticks, got ${delay}`);
  if (!Number.isInteger(t.duration) || t.duration < 0) {
    throw new Error(`duration must be integer ticks >= 0, got ${t.duration}`);
  }
  if (!(iterations === Infinity || (Number.isInteger(iterations) && iterations >= 1))) {
    throw new Error(`iterations must be an integer >= 1 or Infinity, got ${iterations}`);
  }
  return {
    delay,
    duration: t.duration,
    iterations,
    direction: t.direction ?? 'normal',
    fill: t.fill ?? 'none',
  };
}

/** Total active duration in ticks (Infinity when iterations is Infinity). */
export function activeDurationOf(t: NormalizedTiming): Ticks {
  if (t.iterations === Infinity) {
    return t.duration === 0 ? 0 : Infinity;
  }
  return t.duration * t.iterations;
}

export function phaseAt(t: NormalizedTiming, local: Ticks): Phase {
  if (local < t.delay) return 'before';
  if (local - t.delay >= activeDurationOf(t)) return 'after';
  return 'active';
}

/** Whether iteration `iter` plays backwards under the given direction. */
export function isReversedIteration(t: NormalizedTiming, iter: number): boolean {
  switch (t.direction) {
    case 'normal':
      return false;
    case 'reverse':
      return true;
    case 'alternate':
      return iter % 2 === 1;
    case 'alternate-reverse':
      return iter % 2 === 0;
  }
}

function zeroDurationDirected(t: NormalizedTiming, atStart: boolean): Ticks {
  // Defined zero-duration semantics: before the (zero-length) active interval
  // the animation holds its directed start; at/after it, its directed end.
  // 'alternate*' is treated as 'normal' since there is no interior iteration.
  const reversed = t.direction === 'reverse';
  if (atStart) return reversed ? TRACK_END_SENTINEL : TRACK_START_SENTINEL;
  return reversed ? TRACK_START_SENTINEL : TRACK_END_SENTINEL;
}

/**
 * Map a local time to a directed track time (pure function).
 *
 * Returns `null` when the phase is not filled. Reverse playback is true time
 * reversal: the directed time is `duration - iterationTime`, so the reversed
 * curve equals the forward curve reflected in time — values are never
 * swapped between keyframes.
 *
 * Exact period boundary: at local == delay + duration * iterations the
 * animation sits at the END of the last iteration; it never rolls over into
 * an extra round.
 */
export function directedTimeAt(t: NormalizedTiming, local: Ticks): Ticks | null {
  const phase = phaseAt(t, local);
  if (phase === 'before' && !(t.fill === 'backwards' || t.fill === 'both')) return null;
  if (phase === 'after' && !(t.fill === 'forwards' || t.fill === 'both')) return null;

  if (t.duration === 0) {
    return zeroDurationDirected(t, phase === 'before');
  }

  const active = activeDurationOf(t);
  let a = local - t.delay;
  if (a < 0) a = 0;
  if (a > active) a = active;

  let iter: number;
  let iterTime: Ticks;
  if (a === active && t.iterations !== Infinity) {
    // Exact end boundary: clamp to the end of the LAST iteration so an exact
    // multiple of the period never starts one more round.
    iter = t.iterations - 1;
    iterTime = t.duration;
  } else {
    iter = Math.floor(a / t.duration);
    iterTime = a - iter * t.duration;
  }
  return isReversedIteration(t, iter) ? t.duration - iterTime : iterTime;
}

/**
 * Deterministic rest state used by reduced-motion mode: the directed value at
 * the end of the active phase (end of the first iteration when infinite).
 */
export function reducedDirectedTime(t: NormalizedTiming): Ticks {
  if (t.duration === 0) return zeroDurationDirected(t, false);
  const active = activeDurationOf(t);
  if (active === Infinity) {
    // Rest state of an unbounded animation: the end of its first iteration.
    return isReversedIteration(t, 0) ? 0 : t.duration;
  }
  const endLocal = t.delay + active;
  const filled: NormalizedTiming = { ...t, fill: 'both' };
  const directed = directedTimeAt(filled, endLocal);
  return directed === null ? 0 : directed;
}
