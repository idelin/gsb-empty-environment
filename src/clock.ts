export interface Clock {
  now(): number;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
  setTimeout(fn: () => void, ms: number): unknown {
    return setTimeout(fn, ms);
  }
  clearTimeout(handle: unknown): void {
    clearTimeout(handle as NodeJS.Timeout);
  }
}

interface VirtualTimer {
  at: number;
  fn: () => void;
}

export class VirtualClock implements Clock {
  private current: number;
  private seq = 0;
  private timers = new Map<number, VirtualTimer>();

  constructor(start = 0) {
    this.current = start;
  }

  now(): number {
    return this.current;
  }

  setTimeout(fn: () => void, ms: number): unknown {
    const id = ++this.seq;
    this.timers.set(id, { at: this.current + Math.max(0, ms), fn });
    return id;
  }

  clearTimeout(handle: unknown): void {
    this.timers.delete(handle as number);
  }

  get pendingCount(): number {
    return this.timers.size;
  }

  advance(ms: number): void {
    const target = this.current + ms;
    for (;;) {
      let nextId: number | null = null;
      let nextAt = Infinity;
      for (const [id, timer] of this.timers) {
        if (timer.at > target) continue;
        if (timer.at < nextAt || (timer.at === nextAt && (nextId === null || id < nextId))) {
          nextAt = timer.at;
          nextId = id;
        }
      }
      if (nextId === null) break;
      const timer = this.timers.get(nextId);
      if (!timer) break;
      this.timers.delete(nextId);
      this.current = timer.at;
      timer.fn();
    }
    this.current = target;
  }

  runAll(): void {
    while (this.timers.size > 0) {
      let nextAt = Infinity;
      for (const timer of this.timers.values()) {
        if (timer.at < nextAt) nextAt = timer.at;
      }
      this.advance(nextAt - this.current);
    }
  }
}
