import { VirtualClock, TimerHandle } from "./clock";

export type TaskKind = "grid" | "catch-up";

export interface TaskInfo {
  /** Grid instant this run belongs to. For catch-up runs: the time it actually runs. */
  readonly scheduledAt: number;
  /** Virtual time at which the run actually started. */
  readonly startedAt: number;
  /** "grid" = regular on-grid trigger, "catch-up" = coalesced compensation run. */
  readonly kind: TaskKind;
}

export type Task = (info: TaskInfo) => void | Promise<void>;

export interface ScheduleOptions {
  /** Called when a task throws or its promise rejects. Scheduling continues regardless. */
  onError?: (error: unknown, info: TaskInfo) => void;
}

export type TaskState = "active" | "paused" | "stopped";

export interface TaskHandle {
  readonly state: TaskState;
  /** True while the task callback is executing (sync part or pending promise). */
  readonly executing: boolean;
  pause(): void;
  resume(): void;
  stop(): void;
}

class PeriodicTask implements TaskHandle {
  private anchor: number;
  private timer: TimerHandle | null = null;
  private isExecuting = false;
  private currentKind: TaskKind = "grid";
  private missed = false;
  private pausedAt: number | null = null;
  private stopped = false;

  constructor(
    private readonly clock: VirtualClock,
    private readonly task: Task,
    private readonly periodMs: number,
    private readonly onError?: (error: unknown, info: TaskInfo) => void
  ) {
    if (periodMs <= 0) {
      throw new Error("period must be positive");
    }
    this.anchor = clock.now();
    this.scheduleAt(this.anchor + this.periodMs);
  }

  get state(): TaskState {
    if (this.stopped) return "stopped";
    if (this.pausedAt !== null) return "paused";
    return "active";
  }

  get executing(): boolean {
    return this.isExecuting;
  }

  pause(): void {
    if (this.stopped || this.pausedAt !== null) return;
    this.pausedAt = this.clock.now();
    this.cancelTimer();
  }

  resume(): void {
    if (this.stopped || this.pausedAt === null) return;
    // Shift the grid by the paused duration so paused time never accumulates
    // missed triggers, while the original phase is preserved.
    this.anchor += this.clock.now() - this.pausedAt;
    this.pausedAt = null;
    if (this.isExecuting) {
      // The running callback's completion will reschedule.
      return;
    }
    if (this.missed) {
      // A trigger was missed before pausing: run the single coalesced catch-up.
      this.missed = false;
      this.run("catch-up");
      return;
    }
    this.rescheduleOnGrid();
  }

  stop(): void {
    if (this.stopped) return;
    this.stopped = true;
    this.cancelTimer();
    this.missed = false;
    // A callback currently executing is left to finish; nothing is rescheduled.
  }

  private cancelTimer(): void {
    if (this.timer !== null) {
      this.clock.clearTimeout(this.timer);
      this.timer = null;
    }
  }

  private scheduleAt(time: number): void {
    this.cancelTimer();
    this.timer = this.clock.setTimeout(() => this.onGridFire(), time - this.clock.now());
  }

  /** Next grid instant strictly after `from`. */
  private nextGridAfter(from: number): number {
    const k = Math.floor((from - this.anchor) / this.periodMs) + 1;
    return this.anchor + k * this.periodMs;
  }

  private rescheduleOnGrid(): void {
    this.scheduleAt(this.nextGridAfter(this.clock.now()));
  }

  private onGridFire(): void {
    this.timer = null;
    if (this.stopped || this.pausedAt !== null) return;
    if (this.isExecuting) {
      if (this.currentKind === "grid") {
        // A grid task overran its period: coalesce all missed grid instants
        // into a single flag, and keep the grid chain alive so later grid
        // instants are still observed.
        this.missed = true;
        this.scheduleAt(this.clock.now() + this.periodMs);
      }
      // A catch-up run never accumulates further misses: once it finishes,
      // the schedule simply returns to the grid.
      return;
    }
    this.run("grid");
    // If the task is still running (async), keep the grid chain alive so
    // grid instants passing during its execution are observed. For sync
    // tasks, afterRun has already armed the next grid timer.
    if (this.isExecuting && !this.stopped && this.pausedAt === null) {
      this.scheduleAt(this.clock.now() + this.periodMs);
    }
  }

  private run(kind: TaskKind): void {
    const info: TaskInfo = {
      scheduledAt: this.clock.now(),
      startedAt: this.clock.now(),
      kind,
    };
    this.isExecuting = true;
    this.currentKind = kind;
    let result: void | Promise<void>;
    try {
      result = this.task(info);
    } catch (error) {
      this.isExecuting = false;
      this.reportError(error, info);
      this.afterRun();
      return;
    }
    if (result !== undefined && typeof result.then === "function") {
      result.then(
        () => {
          this.isExecuting = false;
          this.afterRun();
        },
        (error: unknown) => {
          this.isExecuting = false;
          this.reportError(error, info);
          this.afterRun();
        }
      );
    } else {
      this.isExecuting = false;
      this.afterRun();
    }
  }

  private afterRun(): void {
    if (this.stopped || this.pausedAt !== null) return;
    if (this.missed) {
      // All missed triggers collapse into exactly one catch-up run.
      this.missed = false;
      this.run("catch-up");
      return;
    }
    // Back to the original time grid.
    this.rescheduleOnGrid();
  }

  private reportError(error: unknown, info: TaskInfo): void {
    if (this.onError !== undefined) {
      this.onError(error, info);
    }
  }
}

export class Scheduler {
  constructor(private readonly clock: VirtualClock) {}

  schedule(task: Task, periodMs: number, options?: ScheduleOptions): TaskHandle {
    return new PeriodicTask(
      this.clock,
      task,
      periodMs,
      options !== undefined ? options.onError : undefined
    );
  }
}
