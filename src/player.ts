import type { Ticks } from './time.ts';
import { ticks } from './time.ts';
import type { AnimNode, PropertyValues } from './animation.ts';
import type { TimelineEvent } from './events.ts';
import { sortForDelivery } from './events.ts';

export type NowFn = () => number;

/**
 * Drives an AnimNode against a clock.
 *
 * Time is never accumulated incrementally: the player re-anchors on every
 * play/pause/resume/rate change, and the current time is always computed as
 * `anchorLocal + (now - anchorNow) * rate` — a single multiply-add. Pause and
 * resume therefore cannot accumulate drift, and sampling stays a pure
 * function of the (exact) current tick, so random access and sequential
 * advancement yield identical values.
 */
export class Player {
  readonly root: AnimNode;
  private now: NowFn;
  private anchorNow: Ticks;
  private anchorLocal: Ticks;
  private playbackRate: number;
  private timeScale: number;
  private playing: boolean;
  private eventMark: Ticks;
  /**
   * Reduced-motion mode: sampling returns the deterministic rest state of
   * every animation (directed end of the active phase), independent of time.
   */
  reducedMotion: boolean;

  constructor(root: AnimNode, now: NowFn = () => Date.now()) {
    this.root = root;
    this.now = now;
    this.anchorNow = ticks(this.now());
    this.anchorLocal = 0;
    this.playbackRate = 1;
    this.timeScale = 1;
    this.playing = true;
    this.eventMark = 0;
    this.reducedMotion = false;
  }

  get effectiveRate(): number {
    return this.playbackRate * this.timeScale;
  }

  get isPlaying(): boolean {
    return this.playing;
  }

  /** Current exact local time of the root node. */
  get currentTicks(): Ticks {
    if (!this.playing) return this.anchorLocal;
    return this.anchorLocal + Math.round((ticks(this.now()) - this.anchorNow) * this.effectiveRate);
  }

  /** Freeze at the current time. Returns the frozen position. */
  pause(): Ticks {
    const t = this.currentTicks;
    this.anchorLocal = t;
    this.anchorNow = ticks(this.now());
    this.playing = false;
    return t;
  }

  /** Resume exactly from the frozen position — no jump, no drift. */
  resume(): void {
    if (this.playing) return;
    this.anchorNow = ticks(this.now());
    this.playing = true;
  }

  /** Random access: jump to an exact local time. */
  seek(t: Ticks): void {
    this.anchorLocal = t;
    this.anchorNow = ticks(this.now());
    this.eventMark = t;
  }

  /** Re-anchor preserving the current position, then change the rate. */
  setPlaybackRate(rate: number): void {
    const t = this.currentTicks;
    this.anchorLocal = t;
    this.anchorNow = ticks(this.now());
    this.playbackRate = rate;
  }

  /** Time scaling composes multiplicatively with the playback rate. */
  setTimeScale(scale: number): void {
    const t = this.currentTicks;
    this.anchorLocal = t;
    this.anchorNow = ticks(this.now());
    this.timeScale = scale;
  }

  /** Pure evaluation at an arbitrary local time (random access). */
  sampleAt(t: Ticks): PropertyValues {
    const out: PropertyValues = {};
    this.root.sampleInto(out, t, this.reducedMotion);
    return out;
  }

  /** Sample at the current time. */
  sample(): PropertyValues {
    return this.sampleAt(this.currentTicks);
  }

  /**
   * Deliver events crossed since the previous drain (or seek), using
   * crossing-interval semantics: boundary events fire exactly once.
   */
  drainEvents(): TimelineEvent[] {
    const to = this.currentTicks;
    const from = this.eventMark;
    const collected: TimelineEvent[] = [];
    this.root.collectEvents(from, to, collected, this.root.id);
    this.eventMark = to;
    return sortForDelivery(collected, from, to);
  }
}
