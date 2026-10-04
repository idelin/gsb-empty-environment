/**
 * Pluggable time source so deadlines can be driven deterministically in tests.
 */
export interface Scheduler {
  now(): number;
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

export class SystemScheduler implements Scheduler {
  now(): number {
    return Date.now();
  }

  setTimeout(callback: () => void, delayMs: number): unknown {
    return setTimeout(callback, delayMs);
  }

  clearTimeout(handle: unknown): void {
    clearTimeout(handle as Parameters<typeof clearTimeout>[0]);
  }
}

interface ManualTimer {
  at: number;
  callback: () => void;
}

/**
 * Deterministic scheduler for tests: time only moves when `advance` is called.
 */
export class ManualScheduler implements Scheduler {
  private currentTime = 0;
  private nextId = 1;
  private timers = new Map<number, ManualTimer>();

  now(): number {
    return this.currentTime;
  }

  setTimeout(callback: () => void, delayMs: number): unknown {
    const id = this.nextId++;
    this.timers.set(id, {
      at: this.currentTime + Math.max(0, delayMs),
      callback,
    });
    return id;
  }

  clearTimeout(handle: unknown): void {
    this.timers.delete(handle as number);
  }

  /** Advance the clock, firing every timer scheduled at or before the target. */
  advance(deltaMs: number): void {
    const target = this.currentTime + deltaMs;
    for (;;) {
      let earliestId: number | null = null;
      let earliest: ManualTimer | null = null;
      for (const [id, timer] of this.timers) {
        if (timer.at <= target && (earliest === null || timer.at < earliest.at)) {
          earliestId = id;
          earliest = timer;
        }
      }
      if (earliestId === null || earliest === null) break;
      this.timers.delete(earliestId);
      this.currentTime = earliest.at;
      earliest.callback();
    }
    this.currentTime = target;
  }

  get pendingCount(): number {
    return this.timers.size;
  }
}
