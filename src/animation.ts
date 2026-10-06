import { PiecewiseTimeMap } from './timeline.js';

export type Direction = 'normal' | 'reverse' | 'alternate' | 'alternate-reverse';
export type PlayState = 'idle' | 'running' | 'paused' | 'finished';

export interface AnimationOptions {
  /** Value at directed progress 0. */
  from: number;
  /** Value at directed progress 1. */
  to: number;
  /** Duration of one iteration in local milliseconds. May be 0. */
  duration: number;
  /** Delay before the active phase, in local milliseconds. Default 0. */
  delay?: number;
  /** Finite iteration count (may be fractional, e.g. 2.5). Default 1. */
  iterations?: number;
  /** Playback direction; 'alternate' is yoyo. Default 'normal'. */
  direction?: Direction;
  /** Local ms per external ms; may be negative. Default 1. */
  playbackRate?: number;
  /** Easing applied to the directed progress. Default linear. */
  easing?: (p: number) => number;
  /** Fired exactly once per natural completion. Never fired on cancel(). */
  onfinish?: (finishTime: number) => void;
}

const linear = (p: number): number => p;

/**
 * Explicit-time animation sampler.
 *
 * All controls (`play`, `pause`, `resume`, `seek`, `setPlaybackRate`,
 * `cancel`) and `sample` take an explicit external timestamp; the library
 * never reads a clock. Local time is derived from a PiecewiseTimeMap, so
 * pause/resume is continuous by construction and any number of operations
 * may share one timestamp.
 *
 * Endpoint ownership inside the active phase:
 *  - An interior iteration boundary (active time exactly k * duration,
 *    k < iterations) belongs to the START of the new iteration
 *    (progress 0 of iteration k).
 *  - The end of the LAST iteration (active time === duration * iterations)
 *    keeps the FINAL value of the last iteration.
 *  - Before the delay the value is the start of iteration 0; at or after
 *    the end the value is the final value. A zero-duration animation
 *    therefore flips from the start value to the final value exactly at
 *    `delay`.
 */
export class Animation {
  private readonly fromValue: number;
  private readonly toValue: number;
  private readonly duration: number;
  private readonly delay: number;
  private readonly iterations: number;
  private readonly direction: Direction;
  private readonly easing: (p: number) => number;
  private readonly onfinish?: (finishTime: number) => void;

  /** Local time at which a positively-playing animation finishes. */
  private readonly endLocal: number;
  private map: PiecewiseTimeMap | null = null;
  private currentRate: number;
  private playState: PlayState = 'idle';
  private finishNotified = false;

  constructor(options: AnimationOptions) {
    const duration = options.duration;
    const delay = options.delay ?? 0;
    const iterations = options.iterations ?? 1;
    if (!(duration >= 0)) throw new RangeError('duration must be >= 0');
    if (!(delay >= 0)) throw new RangeError('delay must be >= 0');
    if (!(iterations > 0) || !Number.isFinite(iterations)) {
      throw new RangeError('iterations must be a finite number > 0');
    }
    const rate = options.playbackRate ?? 1;
    if (!Number.isFinite(rate)) throw new RangeError('playbackRate must be finite');
    this.fromValue = options.from;
    this.toValue = options.to;
    this.duration = duration;
    this.delay = delay;
    this.iterations = iterations;
    this.direction = options.direction ?? 'normal';
    this.easing = options.easing ?? linear;
    this.onfinish = options.onfinish;
    this.currentRate = rate;
    this.endLocal = delay + duration * iterations;
  }

  get state(): PlayState {
    return this.playState;
  }

  /** (Re)starts the animation at external time `t` from local time 0. */
  play(t: number): void {
    this.map = new PiecewiseTimeMap({ startTime: t, startLocal: 0, rate: this.currentRate });
    this.playState = 'running';
    this.finishNotified = false;
  }

  /** Freezes local time at external time `t`. No-op unless running. */
  pause(t: number): void {
    if (this.playState !== 'running' || !this.map) return;
    this.map.cut(t, 0);
    this.playState = 'paused';
  }

  /** Resumes from the frozen local time; sampling is continuous (no jump). */
  resume(t: number): void {
    if (this.playState !== 'paused' || !this.map) return;
    this.map.cut(t, this.currentRate);
    this.playState = 'running';
  }

  /**
   * Sets the local time at external time `t`, clamped to
   * [0, delay + duration * iterations]. Seeking a finished animation
   * re-arms it as paused so it can finish (and notify) again.
   */
  seek(local: number, t: number): void {
    if (!this.map || this.playState === 'idle') return;
    const clamped = Math.min(Math.max(local, 0), this.endLocal);
    const rate = this.playState === 'running' ? this.currentRate : 0;
    this.map.reset(t, clamped, rate);
    if (this.playState === 'finished') {
      this.playState = 'paused';
      this.finishNotified = false;
    }
  }

  /**
   * Changes the playback rate (may be negative) at external time `t`.
   * While running, local time stays continuous; while paused, the new rate
   * takes effect on the next resume().
   */
  setPlaybackRate(rate: number, t: number): void {
    if (!Number.isFinite(rate)) throw new RangeError('playbackRate must be finite');
    this.currentRate = rate;
    if (this.playState === 'running' && this.map) this.map.cut(t, rate);
  }

  /** Cancels the animation: back to idle, no finish event is emitted. */
  cancel(): void {
    this.map = null;
    this.playState = 'idle';
  }

  /** Local animation time at external time `t`, or undefined while idle. */
  localTime(t: number): number | undefined {
    if (!this.map || this.playState === 'idle') return undefined;
    return this.map.map(t);
  }

  /**
   * Samples the value at explicit external time `t`. Returns undefined
   * while idle (never played or cancelled). Detects natural completion
   * lazily: a running animation whose local time reaches the boundary
   * finishes, holds the boundary value, and fires onfinish exactly once.
   */
  sample(t: number): number | undefined {
    if (!this.map || this.playState === 'idle') return undefined;
    let local = this.map.map(t);
    if (this.playState === 'running') {
      if (this.currentRate > 0 && local >= this.endLocal) {
        this.finish(this.endLocal, t);
        local = this.endLocal;
      } else if (this.currentRate < 0 && local <= 0) {
        this.finish(0, t);
        local = 0;
      }
    }
    return this.valueAt(local);
  }

  private finish(boundaryLocal: number, t: number): void {
    const mapped = this.map ? this.map.timeAtLocal(boundaryLocal) : NaN;
    const finishTime = Number.isNaN(mapped) ? t : mapped;
    if (this.map) this.map.reset(finishTime, boundaryLocal, 0);
    this.playState = 'finished';
    if (!this.finishNotified) {
      this.finishNotified = true;
      if (this.onfinish) this.onfinish(finishTime);
    }
  }

  /** Directed progress of iteration `i` at intra-iteration progress `p`. */
  private directedProgress(i: number, p: number): number {
    switch (this.direction) {
      case 'normal':
        return p;
      case 'reverse':
        return 1 - p;
      case 'alternate':
        return i % 2 === 0 ? p : 1 - p;
      case 'alternate-reverse':
        return i % 2 === 0 ? 1 - p : p;
    }
  }

  private valueAt(local: number): number {
    const active = local - this.delay;
    const total = this.duration * this.iterations;
    let directed: number;
    if (active < 0) {
      // Before phase: hold the start of iteration 0.
      directed = this.directedProgress(0, 0);
    } else if (active >= total) {
      // After phase (and the exact end of the last iteration): keep the
      // final value. Also covers duration === 0 for any active >= 0.
      const frac = this.iterations - Math.floor(this.iterations);
      const lastIteration = frac === 0 ? this.iterations - 1 : Math.floor(this.iterations);
      const lastProgress = frac === 0 ? 1 : frac;
      directed = this.directedProgress(lastIteration, lastProgress);
    } else {
      // Interior: floor() assigns an exact iteration boundary to the START
      // of the new iteration (progress 0), never to the previous one.
      const i = Math.floor(active / this.duration);
      const p = (active - i * this.duration) / this.duration;
      directed = this.directedProgress(i, p);
    }
    return this.fromValue + (this.toValue - this.fromValue) * this.easing(directed);
  }
}
