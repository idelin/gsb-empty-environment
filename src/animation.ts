import { KeyframeTrack } from './keyframes.ts';
import type { Keyframe } from './keyframes.ts';

export type Direction = 'normal' | 'reverse' | 'alternate' | 'alternate-reverse';

export interface AnimationOptions {
  keyframes: Keyframe[];
  duration: number;
  delay?: number;
  iterations?: number;
  direction?: Direction;
  keyframeEvents?: boolean;
  reducedMotion?: boolean;
}

export interface AnimationEvent {
  time: number;
  name: string;
  data?: unknown;
}

interface StoredEvent extends AnimationEvent {
  version: number;
}

// A keyframe animation. `valueAt(t)` is a pure function of local time `t`:
// arbitrary seeking and sequential advancement always agree, and no state is
// accumulated between evaluations.
export class KeyframeAnimation {
  readonly duration: number;
  readonly delay: number;
  readonly iterations: number;
  readonly direction: Direction;
  keyframeEvents: boolean;
  reducedMotion: boolean;

  private track: KeyframeTrack;
  private version = 0;
  private storedEvents: StoredEvent[] = [];

  constructor(options: AnimationOptions) {
    if (!(options.duration >= 0)) throw new Error('duration must be >= 0');
    if (!(options.iterations === undefined || options.iterations >= 1)) {
      throw new Error('iterations must be >= 1');
    }
    this.track = new KeyframeTrack(options.keyframes);
    this.duration = options.duration;
    this.delay = options.delay ?? 0; // negative delay is defined: the animation starts partway
    this.iterations = options.iterations ?? 1;
    this.direction = options.direction ?? 'normal';
    this.keyframeEvents = options.keyframeEvents ?? false;
    this.reducedMotion = options.reducedMotion ?? false;
  }

  get totalDuration(): number {
    return this.duration * this.iterations;
  }

  get keyframesVersion(): number {
    return this.version;
  }

  // Hot-swap keyframes. Events registered against an older keyframe
  // generation are invalidated; the play position is untouched, so playback
  // resumes from the same local time against the new track.
  setKeyframes(keyframes: Keyframe[]): void {
    this.track = new KeyframeTrack(keyframes);
    this.version += 1;
    this.storedEvents = this.storedEvents.filter((e) => e.version === this.version);
  }

  addEvent(time: number, name: string, data?: unknown): void {
    this.storedEvents.push({ time, name, data, version: this.version });
  }

  private directedProgress(iteration: number, progress: number): number {
    const reversed =
      this.direction === 'reverse' ||
      (this.direction === 'alternate' && iteration % 2 === 1) ||
      (this.direction === 'alternate-reverse' && iteration % 2 === 0);
    // Time reversal of the forward track: sample(1 - p) mirrors time exactly,
    // it does not approximate reversal by swapping keyframe values.
    return reversed ? 1 - progress : progress;
  }

  startState(): Record<string, number> {
    return this.track.sample(this.directedProgress(0, 0));
  }

  endState(): Record<string, number> {
    const lastIteration = Number.isFinite(this.iterations) ? this.iterations - 1 : 0;
    return this.track.sample(this.directedProgress(lastIteration, 1));
  }

  // Exact period boundary: active time == totalDuration resolves to the end
  // state of the final iteration, never to the start of an extra round.
  private iterationProgress(active: number): { iteration: number; progress: number } | null {
    if (active < 0) return null;
    if (this.duration === 0) return { iteration: 0, progress: 1 };
    const scaled = active / this.duration;
    if (scaled >= this.iterations) return null;
    const iteration = Math.floor(scaled);
    return { iteration, progress: scaled - iteration };
  }

  valueAt(t: number): Record<string, number> {
    if (this.reducedMotion) return this.endState(); // deterministic reduced-motion output
    const active = t - this.delay;
    if (this.duration === 0) {
      // Zero duration is defined: before the delay the start state holds,
      // at/after the delay the end state holds.
      return active < 0 ? this.startState() : this.endState();
    }
    const ip = this.iterationProgress(active);
    if (ip === null) return active < 0 ? this.startState() : this.endState();
    return this.track.sample(this.directedProgress(ip.iteration, ip.progress));
  }

  // Cross-interval event semantics: advancing from t0 to t1 delivers events
  // whose time lies in (t0, t1] when moving forward and in [t1, t0) when
  // moving backward. An event exactly on a shared boundary belongs to exactly
  // one of two adjacent intervals, so boundary events never fire twice.
  eventsInInterval(t0: number, t1: number): AnimationEvent[] {
    if (this.reducedMotion || t0 === t1) return [];
    const forward = t1 > t0;
    const lo = Math.min(t0, t1);
    const hi = Math.max(t0, t1);
    const inInterval = (te: number) => (forward ? te > lo && te <= hi : te >= lo && te < hi);

    const out: AnimationEvent[] = [];
    for (const e of this.storedEvents) {
      if (inInterval(e.time)) out.push({ time: e.time, name: e.name, data: e.data });
    }
    if (this.keyframeEvents && this.duration > 0) {
      const firstIter = Math.max(0, Math.floor((lo - this.delay) / this.duration) - 1);
      const lastIter = Math.min(this.iterations - 1, Math.ceil((hi - this.delay) / this.duration) + 1);
      for (let i = firstIter; i <= lastIter; i++) {
        for (const kf of this.track.keyframes) {
          const te = this.delay + (i + kf.offset) * this.duration;
          if (inInterval(te)) {
            out.push({ time: te, name: 'keyframe', data: { iteration: i, offset: kf.offset } });
          }
        }
      }
    }
    out.sort((a, b) => (forward ? a.time - b.time : b.time - a.time));
    return out;
  }
}
