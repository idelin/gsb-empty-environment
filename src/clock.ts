/**
 * Injectable clock. The announcer never touches Date.now()/setTimeout
 * directly, so tests can drive time deterministically with ManualClock.
 */

export type TimerHandle = number;

export interface Clock {
  now(): number;
  setTimeout(callback: () => void, delayMs: number): TimerHandle;
  clearTimeout(handle: TimerHandle): void;
}

export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
  setTimeout(callback: () => void, delayMs: number): TimerHandle {
    return setTimeout(callback, delayMs) as unknown as TimerHandle;
  }
  clearTimeout(handle: TimerHandle): void {
    clearTimeout(handle as unknown as NodeJS.Timeout);
  }
}

interface PendingTimer {
  handle: TimerHandle;
  fireAt: number;
  callback: () => void;
}

/** Deterministic clock for tests and demos. Time only moves via advance(). */
export class ManualClock implements Clock {
  private current = 0;
  private nextHandle = 1;
  private timers: PendingTimer[] = [];

  now(): number {
    return this.current;
  }

  setTimeout(callback: () => void, delayMs: number): TimerHandle {
    const handle = this.nextHandle++;
    this.timers.push({ handle, fireAt: this.current + Math.max(0, delayMs), callback });
    return handle;
  }

  clearTimeout(handle: TimerHandle): void {
    this.timers = this.timers.filter((t) => t.handle !== handle);
  }

  get pendingCount(): number {
    return this.timers.length;
  }

  /** Advance time, firing every timer scheduled at or before the target. */
  advance(deltaMs: number): void {
    const target = this.current + deltaMs;
    for (;;) {
      let next: PendingTimer | undefined;
      for (const t of this.timers) {
        if (t.fireAt <= target && (next === undefined || t.fireAt < next.fireAt)) {
          next = t;
        }
      }
      if (!next) break;
      this.current = next.fireAt;
      this.timers = this.timers.filter((t) => t.handle !== next!.handle);
      next.callback();
    }
    this.current = target;
  }

  /** Fire the next pending timer. Returns false when nothing is scheduled. */
  step(): boolean {
    let next: PendingTimer | undefined;
    for (const t of this.timers) {
      if (next === undefined || t.fireAt < next.fireAt) next = t;
    }
    if (!next) return false;
    this.current = next.fireAt;
    this.timers = this.timers.filter((t) => t.handle !== next!.handle);
    next.callback();
    return true;
  }

  /** Run until no timers remain (or the safety cap is hit). */
  runUntilIdle(maxSteps = 10_000): void {
    let steps = 0;
    while (this.step()) {
      if (++steps > maxSteps) throw new Error('ManualClock: runaway timers');
    }
  }
}
