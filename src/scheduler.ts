import { VirtualClock } from './clock.ts';

export type RunKind = 'tick' | 'catch-up';

export interface RunContext {
  /** 'tick' = on-grid run, 'catch-up' = single coalesced compensation run. */
  readonly kind: RunKind;
  /** Grid time this run belongs to (for catch-up: the latest coalesced grid point). */
  readonly scheduledAt: number;
  /** Virtual time at which the callback actually started. */
  readonly startedAt: number;
  /** Number of grid triggers coalesced into this run (0 for a normal tick). */
  readonly missedCount: number;
}

export type TaskCallback = (ctx: RunContext) => void | Promise<void>;

export interface PeriodicSchedulerOptions {
  /** Distance between grid points, in virtual milliseconds. Must be > 0. */
  periodMs: number;
  /** Grid anchor. Grid points are startAt + k * periodMs (k >= 1). Defaults to clock.now(). */
  startAt?: number;
  /** Called when a run throws or rejects; scheduling continues regardless. */
  onError?: (error: unknown, ctx: RunContext) => void;
}

/**
 * Fixed-start periodic scheduler driven by a VirtualClock.
 *
 * Semantics:
 * - Triggers sit on a fixed grid anchored at `startAt`; the grid never shifts.
 * - If a run is still in flight when grid points pass, all missed triggers
 *   coalesce into exactly one catch-up run, started immediately after the
 *   current run settles. Afterwards scheduling rejoins the original grid.
 * - `pause()` drops everything: grid points that elapse while paused are not
 *   accumulated, and `resume()` simply waits for the next future grid point.
 * - `stop()` cancels future triggers; an in-flight callback runs to completion.
 */
export class PeriodicScheduler {
  private readonly clock: VirtualClock;
  private readonly task: TaskCallback;
  private readonly periodMs: number;
  private readonly startAt: number;
  private readonly onError?: (error: unknown, ctx: RunContext) => void;

  /** Largest grid point already covered (fired, coalesced, or dropped). */
  private coveredUntil: number;
  private wakeupId: number | null = null;
  private inFlight = 0;
  private idleWaiters: Array<() => void> = [];
  private paused = false;
  private stopped = false;

  constructor(clock: VirtualClock, task: TaskCallback, options: PeriodicSchedulerOptions) {
    if (!Number.isFinite(options.periodMs) || options.periodMs <= 0) {
      throw new RangeError(`periodMs must be a finite number > 0, got ${options.periodMs}`);
    }
    this.clock = clock;
    this.task = task;
    this.periodMs = options.periodMs;
    this.startAt = options.startAt ?? clock.now();
    this.onError = options.onError;
    this.coveredUntil = this.startAt;
    this.scheduleWakeup();
  }

  get isPaused(): boolean {
    return this.paused;
  }

  get isStopped(): boolean {
    return this.stopped;
  }

  /** True while a callback (tick or catch-up) is executing. */
  get isRunning(): boolean {
    return this.inFlight > 0;
  }

  /** Virtual time of the next scheduled wakeup, or null if none is pending. */
  get nextRunAt(): number | null {
    if (this.stopped || this.paused || this.wakeupId === null) return null;
    return this.coveredUntil + this.periodMs;
  }

  pause(): void {
    if (this.stopped || this.paused) return;
    this.paused = true;
    this.clearWakeup();
  }

  resume(): void {
    if (this.stopped || !this.paused) return;
    this.paused = false;
    // Grid points that elapsed while paused are dropped, not accumulated.
    const gridFloor = this.gridFloor(this.clock.now());
    if (gridFloor > this.coveredUntil) this.coveredUntil = gridFloor;
    if (this.inFlight === 0) this.scheduleWakeup();
  }

  stop(): void {
    if (this.stopped) return;
    this.stopped = true;
    this.clearWakeup();
  }

  /** Resolves once no callback is in flight (immediately if already idle). */
  idle(): Promise<void> {
    if (this.inFlight === 0) return Promise.resolve();
    return new Promise<void>((resolve) => this.idleWaiters.push(resolve));
  }

  private gridFloor(t: number): number {
    return this.startAt + Math.floor((t - this.startAt) / this.periodMs) * this.periodMs;
  }

  private clearWakeup(): void {
    if (this.wakeupId !== null) {
      this.clock.clearTimeout(this.wakeupId);
      this.wakeupId = null;
    }
  }

  private scheduleWakeup(): void {
    if (this.stopped || this.paused) return;
    const next = this.coveredUntil + this.periodMs;
    const delay = Math.max(0, next - this.clock.now());
    this.wakeupId = this.clock.setTimeout(() => this.onWakeup(next), delay);
  }

  private onWakeup(scheduledAt: number): void {
    this.wakeupId = null;
    if (this.stopped || this.paused) return;
    this.coveredUntil = scheduledAt;
    this.startRun({ kind: 'tick', scheduledAt, startedAt: this.clock.now(), missedCount: 0 });
  }

  private startRun(ctx: RunContext): void {
    this.inFlight += 1;
    let result: void | Promise<void>;
    try {
      result = this.task(ctx);
    } catch (error) {
      this.reportError(error, ctx);
      result = undefined;
    }
    if (result !== undefined && result !== null && typeof result.then === 'function') {
      result.then(
        () => this.onSettle(),
        (error) => {
          this.reportError(error, ctx);
          this.onSettle();
        },
      );
    } else {
      // Synchronous tasks settle immediately, so on-grid ticks chain within
      // a single clock advance instead of being seen as overruns.
      this.onSettle();
    }
  }

  private onSettle(): void {
    this.inFlight -= 1;
    if (this.inFlight === 0) {
      const waiters = this.idleWaiters;
      this.idleWaiters = [];
      for (const resolve of waiters) resolve();
    }
    if (this.stopped || this.paused) return;
    const now = this.clock.now();
    if (this.coveredUntil + this.periodMs <= now) {
      // The run overran at least one grid point: every missed trigger is
      // coalesced into a single catch-up run, fired immediately.
      const coalescedThrough = this.gridFloor(now);
      const missedCount = Math.round((coalescedThrough - this.coveredUntil) / this.periodMs);
      this.coveredUntil = coalescedThrough;
      this.startRun({ kind: 'catch-up', scheduledAt: coalescedThrough, startedAt: now, missedCount });
      return;
    }
    this.scheduleWakeup();
  }

  private reportError(error: unknown, ctx: RunContext): void {
    if (this.onError) this.onError(error, ctx);
  }
}
