export type Easing = (t: number) => number;
export type Direction = "normal" | "alternate";
export type AnimationState = "idle" | "running" | "paused" | "finished";

export interface AnimationOptions {
  from: number;
  to: number;
  /** Duration of one iteration in milliseconds. May be 0 (step animation). */
  duration: number;
  /** Delay in milliseconds before the active phase starts. Defaults to 0. */
  delay?: number;
  /** Finite repeat count, may be fractional (e.g. 2.5). Defaults to 1. */
  iterations?: number;
  /** "alternate" is yoyo: odd iterations play in reverse. Defaults to "normal". */
  direction?: Direction;
  /** Easing applied to the directed in-iteration phase. Defaults to linear. */
  easing?: Easing;
  /** Playback rate; negative values play the timeline in reverse. Defaults to 1. */
  playbackRate?: number;
}

/**
 * One piece of the piecewise-linear global->local time map.
 * Local time at global time t (within this segment) is
 * `startLocal + (t - startGlobal) * rate`.
 */
export interface Segment {
  readonly startGlobal: number;
  readonly startLocal: number;
  readonly rate: number;
}

export interface FinishEvent {
  readonly globalTime: number;
  readonly localTime: number;
}

export interface TimingInfo {
  readonly delay: number;
  readonly duration: number;
  readonly iterations: number;
  readonly activeDuration: number;
  readonly endTime: number;
}

type FinishListener = (event: FinishEvent) => void;

const linear: Easing = (t) => t;

function assertFinite(name: string, value: number): void {
  if (!Number.isFinite(value)) {
    throw new TypeError(`${name} must be a finite number, got ${value}`);
  }
}

/**
 * A deterministic, explicit-time animation.
 *
 * The wall clock is never read; every operation and every sample takes an
 * explicit timestamp, so a fixed sequence of calls always produces the same
 * values. `sample(now)` is also the driver: it is what detects that the
 * finish boundary has been crossed and emits "finish".
 *
 * Operation timestamps (play/pause/setPlaybackRate) must be non-decreasing.
 */
export class Animation {
  private readonly fromValue: number;
  private readonly toValue: number;
  private readonly duration: number;
  private readonly delay: number;
  private readonly iterations: number;
  private readonly direction: Direction;
  private readonly easing: Easing;
  private readonly activeDuration: number;
  private readonly endTime: number;
  private playbackRate: number;
  private state: AnimationState = "idle";
  private segments: Segment[] = [];
  private finishedLocal = 0;
  private finishListeners: FinishListener[] = [];

  constructor(options: AnimationOptions) {
    const {
      from,
      to,
      duration,
      delay = 0,
      iterations = 1,
      direction = "normal",
      easing = linear,
      playbackRate = 1,
    } = options;
    assertFinite("from", from);
    assertFinite("to", to);
    assertFinite("duration", duration);
    assertFinite("delay", delay);
    assertFinite("iterations", iterations);
    assertFinite("playbackRate", playbackRate);
    if (duration < 0) throw new RangeError("duration must be >= 0");
    if (delay < 0) throw new RangeError("delay must be >= 0");
    if (iterations <= 0) throw new RangeError("iterations must be > 0");

    this.fromValue = from;
    this.toValue = to;
    this.duration = duration;
    this.delay = delay;
    this.iterations = iterations;
    this.direction = direction;
    this.easing = easing;
    this.playbackRate = playbackRate;
    this.activeDuration = duration * iterations;
    this.endTime = delay + this.activeDuration;
  }

  get currentState(): AnimationState {
    return this.state;
  }

  get currentPlaybackRate(): number {
    return this.playbackRate;
  }

  get timing(): TimingInfo {
    return {
      delay: this.delay,
      duration: this.duration,
      iterations: this.iterations,
      activeDuration: this.activeDuration,
      endTime: this.endTime,
    };
  }

  on(event: "finish", listener: FinishListener): void {
    if (event === "finish") this.finishListeners.push(listener);
  }

  off(event: "finish", listener: FinishListener): void {
    if (event === "finish") {
      this.finishListeners = this.finishListeners.filter((l) => l !== listener);
    }
  }

  /**
   * Starts (from idle/finished) or resumes (from paused) the animation.
   * A fresh start begins at local time 0 for non-negative rates and at the
   * end of the active phase for negative rates.
   */
  play(now: number): void {
    assertFinite("now", now);
    if (this.state === "running") return;
    if (this.state === "paused") {
      this.pushSegment(now, this.playbackRate);
      this.state = "running";
      return;
    }
    const startLocal = this.playbackRate >= 0 ? 0 : this.endTime;
    this.segments = [{ startGlobal: now, startLocal, rate: this.playbackRate }];
    this.state = "running";
  }

  /** Freezes local time. No-op unless running. */
  pause(now: number): void {
    assertFinite("now", now);
    if (this.state !== "running") return;
    this.pushSegment(now, 0);
    this.state = "paused";
  }

  /**
   * Changes the playback rate. While running, local time stays continuous
   * (a new segment starts at the current local time). While paused, the new
   * rate takes effect on the next play().
   */
  setPlaybackRate(rate: number, now: number): void {
    assertFinite("rate", rate);
    assertFinite("now", now);
    const last = this.segments[this.segments.length - 1];
    if (last && now < last.startGlobal) {
      throw new RangeError(
        `operation time must not go backward (last op at ${last.startGlobal}, got ${now})`,
      );
    }
    this.playbackRate = rate;
    if (this.state === "running") this.pushSegment(now, rate);
  }

  /**
   * Resets to idle and clears the timeline. Never emits "finish".
   */
  cancel(): void {
    this.state = "idle";
    this.segments = [];
    this.finishedLocal = 0;
  }

  /**
   * Samples the value at an explicit global time. While running, this also
   * detects crossing the finish boundary (local time >= endTime for positive
   * rates, <= 0 for negative rates), transitions to "finished" and emits
   * "finish" exactly once. Returns undefined while idle.
   */
  sample(now: number): number | undefined {
    assertFinite("now", now);
    switch (this.state) {
      case "idle":
        return undefined;
      case "paused":
        return this.valueAtLocal(this.localTimeAt(now) as number);
      case "finished":
        return this.valueAtLocal(this.finishedLocal);
      case "running": {
        const local = this.localTimeAt(now) as number;
        const rate = this.currentRate();
        if (rate > 0 && local >= this.endTime) {
          return this.finish(now, this.endTime);
        }
        if (rate < 0 && local <= 0) {
          return this.finish(now, 0);
        }
        return this.valueAtLocal(local);
      }
    }
  }

  /**
   * Maps a global time to local animation time through the piecewise
   * segment map. Returns undefined while idle (no segments).
   */
  localTimeAt(now: number): number | undefined {
    if (this.segments.length === 0) return undefined;
    let segment = this.segments[0];
    if (now < segment.startGlobal) return segment.startLocal;
    for (let i = this.segments.length - 1; i >= 0; i--) {
      if (this.segments[i].startGlobal <= now) {
        segment = this.segments[i];
        break;
      }
    }
    return segment.startLocal + (now - segment.startGlobal) * segment.rate;
  }

  /** Copy of the piecewise time map, for inspection and testing. */
  getSegments(): Segment[] {
    return this.segments.map((s) => ({ ...s }));
  }

  private currentRate(): number {
    return this.segments[this.segments.length - 1].rate;
  }

  /**
   * Appends a segment starting at `now` with the given rate, keeping local
   * time continuous. Multiple operations at the same timestamp collapse into
   * a single segment: the last one wins, no zero-length segments are kept.
   */
  private pushSegment(now: number, rate: number): void {
    const local = this.localTimeAt(now) as number;
    const last = this.segments[this.segments.length - 1];
    if (last && now < last.startGlobal) {
      throw new RangeError(
        `operation time must not go backward (last op at ${last.startGlobal}, got ${now})`,
      );
    }
    const segment: Segment = { startGlobal: now, startLocal: local, rate };
    if (last && now === last.startGlobal) {
      this.segments[this.segments.length - 1] = segment;
    } else {
      this.segments.push(segment);
    }
  }

  private finish(now: number, local: number): number {
    this.state = "finished";
    this.finishedLocal = local;
    const event: FinishEvent = { globalTime: now, localTime: local };
    for (const listener of [...this.finishListeners]) listener(event);
    return this.valueAtLocal(local);
  }

  /**
   * Endpoint ownership: inside the active phase, an exact period boundary
   * belongs to the START of the new period (floor gives the new iteration,
   * phase 0). The end of the LAST period is the single exception: local
   * times >= endTime keep the final value of the last iteration.
   */
  private valueAtLocal(local: number): number {
    let phase: number;
    if (local < this.delay) {
      phase = 0;
    } else if (local >= this.endTime) {
      phase = this.finalDirectedPhase();
    } else {
      const t = local - this.delay;
      const iteration = Math.floor(t / this.duration);
      const raw = (t - iteration * this.duration) / this.duration;
      phase = this.applyDirection(iteration, raw);
    }
    return this.fromValue + (this.toValue - this.fromValue) * this.easing(phase);
  }

  /**
   * Directed phase at the very end of the active phase. For integer
   * iteration counts this is the end of the last full iteration; for
   * fractional counts it is mid-iteration. With "alternate", the last
   * iteration may be a reversed one, so the final value can be `from`.
   */
  private finalDirectedPhase(): number {
    const lastIteration = Math.ceil(this.iterations) - 1;
    const raw = this.iterations - lastIteration;
    return this.applyDirection(lastIteration, raw);
  }

  private applyDirection(iteration: number, raw: number): number {
    return this.direction === "alternate" && iteration % 2 === 1
      ? 1 - raw
      : raw;
  }
}
