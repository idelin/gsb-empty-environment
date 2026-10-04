export interface TimerHandle {
  readonly id: number;
}

interface TimerEntry {
  id: number;
  time: number;
  callback: () => void;
  cancelled: boolean;
}

/**
 * A deterministic virtual clock. Time only moves when `advance` is called
 * (typically from tests). Due timers fire in (time, insertion) order.
 */
export class VirtualClock {
  private currentTime = 0;
  private nextId = 1;
  private timers: TimerEntry[] = [];

  now(): number {
    return this.currentTime;
  }

  setTimeout(callback: () => void, delayMs: number): TimerHandle {
    const entry: TimerEntry = {
      id: this.nextId++,
      time: this.currentTime + Math.max(0, delayMs),
      callback,
      cancelled: false,
    };
    this.timers.push(entry);
    return { id: entry.id };
  }

  clearTimeout(handle: TimerHandle): void {
    for (const entry of this.timers) {
      if (entry.id === handle.id) {
        entry.cancelled = true;
      }
    }
  }

  /** Number of live (not yet fired / cancelled) timers. */
  pendingCount(): number {
    return this.timers.filter((t) => !t.cancelled).length;
  }

  advance(deltaMs: number): void {
    if (deltaMs < 0) {
      throw new Error("cannot advance clock by a negative amount");
    }
    const target = this.currentTime + deltaMs;
    for (;;) {
      let next: TimerEntry | null = null;
      for (const entry of this.timers) {
        if (entry.cancelled || entry.time > target) continue;
        if (
          next === null ||
          entry.time < next.time ||
          (entry.time === next.time && entry.id < next.id)
        ) {
          next = entry;
        }
      }
      if (next === null) break;
      next.cancelled = true;
      this.currentTime = next.time;
      next.callback();
    }
    this.timers = this.timers.filter((t) => !t.cancelled);
    this.currentTime = target;
  }
}
