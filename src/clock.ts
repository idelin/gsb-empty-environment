export type TimerCallback = () => void;

interface Timer {
  id: number;
  at: number;
  seq: number;
  cb: TimerCallback;
  cancelled: boolean;
}

/**
 * A deterministic virtual clock. Time never moves on its own; tests advance
 * it explicitly with `advanceBy` / `advanceTo`, which fire due timers in
 * (time, insertion-order) sequence.
 */
export class VirtualClock {
  private currentTime: number;
  private timers: Timer[] = [];
  private nextId = 1;
  private nextSeq = 0;

  constructor(startTime = 0) {
    this.currentTime = startTime;
  }

  now(): number {
    return this.currentTime;
  }

  setTimeout(cb: TimerCallback, delayMs: number): number {
    if (!Number.isFinite(delayMs) || delayMs < 0) {
      throw new RangeError(`delayMs must be a finite number >= 0, got ${delayMs}`);
    }
    const timer: Timer = {
      id: this.nextId++,
      at: this.currentTime + delayMs,
      seq: this.nextSeq++,
      cb,
      cancelled: false,
    };
    this.timers.push(timer);
    return timer.id;
  }

  clearTimeout(id: number): void {
    const timer = this.timers.find((t) => t.id === id);
    if (timer) timer.cancelled = true;
  }

  get pendingCount(): number {
    return this.timers.reduce((count, t) => (t.cancelled ? count : count + 1), 0);
  }

  advanceBy(ms: number): void {
    this.advanceTo(this.currentTime + ms);
  }

  advanceTo(target: number): void {
    if (target < this.currentTime) {
      throw new RangeError(
        `cannot move the clock backwards (from ${this.currentTime} to ${target})`,
      );
    }
    for (;;) {
      let next: Timer | undefined;
      for (const timer of this.timers) {
        if (timer.cancelled || timer.at > target) continue;
        if (
          next === undefined ||
          timer.at < next.at ||
          (timer.at === next.at && timer.seq < next.seq)
        ) {
          next = timer;
        }
      }
      if (next === undefined) break;
      this.currentTime = next.at;
      next.cancelled = true;
      this.timers.splice(this.timers.indexOf(next), 1);
      next.cb();
    }
    this.currentTime = target;
  }
}
