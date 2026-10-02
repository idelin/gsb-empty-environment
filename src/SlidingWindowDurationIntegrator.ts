/**
 * SlidingWindowDurationIntegrator
 *
 * Computes the cumulative milliseconds within a sliding time window during
 * which a streaming metric satisfies a boolean condition.
 *
 * The metric is treated as a step function: each sample defines the metric
 * value (and thus the condition state) from its timestamp until the next
 * sample. State changes are recorded as discrete transition points, so when
 * the window slides past a transition its contribution is deducted exactly
 * (integer-millisecond arithmetic) with no numerical-integration drift.
 */
export interface Transition {
  /** Timestamp (ms) at which the condition state becomes `active`. */
  readonly t: number;
  readonly active: boolean;
}

export class SlidingWindowDurationIntegrator {
  private readonly windowMs: number;
  private readonly condition: (value: number) => boolean;

  /**
   * Ordered list of state transitions. Invariant: consecutive entries have
   * alternating `active` values, and the state before the first entry is
   * `false` (condition not satisfied).
   */
  private transitions: Transition[] = [];

  /** Current condition state (after the most recent sample). */
  private active = false;

  /** Timestamp of the most recent sample; samples must be non-decreasing. */
  private lastSampleT: number | null = null;

  constructor(windowMs: number, condition: (value: number) => boolean) {
    if (!Number.isFinite(windowMs) || windowMs <= 0) {
      throw new RangeError("windowMs must be a positive finite number");
    }
    this.windowMs = windowMs;
    this.condition = condition;
  }

  /** Feed one metric sample at timestamp `tMs` (milliseconds). */
  addSample(tMs: number, value: number): void {
    if (this.lastSampleT !== null && tMs < this.lastSampleT) {
      throw new RangeError(
        `samples must arrive in non-decreasing time order (got ${tMs} after ${this.lastSampleT})`,
      );
    }
    this.lastSampleT = tMs;

    const nowActive = this.condition(value);
    if (this.transitions.length === 0 && !this.active) {
      // First sample: only record a transition if the condition turns on.
      if (nowActive) {
        this.transitions.push({ t: tMs, active: true });
        this.active = true;
      }
      return;
    }
    if (nowActive !== this.active) {
      this.transitions.push({ t: tMs, active: nowActive });
      this.active = nowActive;
    }
  }

  /**
   * Total milliseconds within the window ending at `nowMs`
   * (i.e. the half-open interval [nowMs - windowMs, nowMs)) during which
   * the condition held. The current state persists up to `nowMs`.
   */
  totalActiveMs(nowMs: number): number {
    const windowStart = nowMs - this.windowMs;
    this.prune(windowStart);

    let total = 0;
    for (let i = 0; i < this.transitions.length; i++) {
      const seg = this.transitions[i];
      if (!seg.active) continue;
      const segEnd = i + 1 < this.transitions.length ? this.transitions[i + 1].t : nowMs;
      const start = Math.max(seg.t, windowStart);
      const end = Math.min(segEnd, nowMs);
      if (end > start) total += end - start;
    }
    return total;
  }

  /**
   * Drop transitions that can no longer affect any future query: all but the
   * last transition at or before `windowStart` (the kept one defines the
   * state at the window's left edge).
   */
  private prune(windowStart: number): void {
    let dropCount = 0;
    for (let i = 0; i < this.transitions.length - 1; i++) {
      if (this.transitions[i + 1].t <= windowStart) dropCount = i + 1;
      else break;
    }
    if (dropCount > 0) this.transitions.splice(0, dropCount);
  }
}
