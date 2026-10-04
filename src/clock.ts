export interface TimerHandle {
  readonly id: number;
}

export interface Clock {
  now(): number;
  setTimer(at: number, callback: () => void): TimerHandle;
  clearTimer(handle: TimerHandle): void;
}

export class SystemClock implements Clock {
  private nextId = 1;
  private readonly handles = new Map<number, NodeJS.Timeout>();

  now(): number {
    return Date.now();
  }

  setTimer(at: number, callback: () => void): TimerHandle {
    const id = this.nextId++;
    const delay = Math.max(0, at - this.now());
    this.handles.set(id, setTimeout(() => {
      this.handles.delete(id);
      callback();
    }, delay));
    return { id };
  }

  clearTimer(handle: TimerHandle): void {
    const timer = this.handles.get(handle.id);
    if (timer !== undefined) {
      clearTimeout(timer);
      this.handles.delete(handle.id);
    }
  }
}

interface VirtualTimer {
  at: number;
  callback: () => void;
}

export class VirtualClock implements Clock {
  private current: number;
  private nextId = 1;
  private readonly timers = new Map<number, VirtualTimer>();

  constructor(start = 0) {
    this.current = start;
  }

  now(): number {
    return this.current;
  }

  setTimer(at: number, callback: () => void): TimerHandle {
    const id = this.nextId++;
    this.timers.set(id, { at, callback });
    return { id };
  }

  clearTimer(handle: TimerHandle): void {
    this.timers.delete(handle.id);
  }

  get pendingCount(): number {
    return this.timers.size;
  }

  advanceBy(ms: number): void {
    this.advanceTo(this.current + ms);
  }

  advanceTo(target: number): void {
    if (target < this.current) {
      throw new Error(`cannot move clock backwards: ${target} < ${this.current}`);
    }
    for (;;) {
      let nextId: number | undefined;
      let nextAt = Infinity;
      for (const [id, timer] of this.timers) {
        if (timer.at <= target && timer.at < nextAt) {
          nextAt = timer.at;
          nextId = id;
        }
      }
      if (nextId === undefined) break;
      const timer = this.timers.get(nextId)!;
      this.timers.delete(nextId);
      if (timer.at > this.current) this.current = timer.at;
      timer.callback();
    }
    this.current = target;
  }
}
