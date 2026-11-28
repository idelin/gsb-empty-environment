/**
 * Deterministic virtual clock and min-heap timer scheduler. Using an injectable
 * clock makes escalation timelines fully reproducible in tests without real
 * waiting, while still allowing the real system clock in production.
 */

export interface Clock {
  now(): number;
}

/** Clock backed by the real system time. */
export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
}

/** Clock whose time is advanced explicitly by the driver/test. */
export class VirtualClock implements Clock {
  private currentMs: number;

  constructor(startMs: number) {
    this.currentMs = startMs;
  }

  now(): number {
    return this.currentMs;
  }

  setTime(ms: number): void {
    this.currentMs = ms;
  }

  advance(ms: number): void {
    this.currentMs += ms;
  }
}

interface PendingTimer {
  id: number;
  fireAtMs: number;
  callback: () => void;
  cancelled: boolean;
}

/**
 * Manual timer wheel: timers are registered against a clock and fired when the
 * clock is advanced to/past their deadline via `runDue`.
 */
export class Scheduler {
  private readonly clock: Clock;
  private timers: PendingTimer[] = [];
  private nextId = 1;

  constructor(clock: Clock) {
    this.clock = clock;
  }

  /** Schedule `callback` after `delayMs` of clock time. Returns a timer id. */
  schedule(delayMs: number, callback: () => void): number {
    const timer: PendingTimer = {
      id: this.nextId++,
      fireAtMs: this.clock.now() + Math.max(0, delayMs),
      callback,
      cancelled: false,
    };
    this.timers.push(timer);
    this.heapPush(timer);
    return timer.id;
  }

  /** Cancel a timer. Cancelling an unknown/already-fired id is a no-op. */
  cancel(id: number): boolean {
    const timer = this.timers.find((t) => t.id === id);
    if (!timer || timer.cancelled) return false;
    timer.cancelled = true;
    return true;
  }

  cancelAll(): number {
    let count = 0;
    for (const timer of this.timers) {
      if (!timer.cancelled) {
        timer.cancelled = true;
        count += 1;
      }
    }
    return count;
  }

  /** Number of currently armed (non-cancelled, not-yet-due) timers. */
  pendingCount(): number {
    return this.timers.filter((t) => !t.cancelled && t.fireAtMs > this.clock.now())
      .length;
  }

  /** Fire every due timer once in chronological order. */
  runDue(): void {
    const due: PendingTimer[] = [];
    while (this.heap.length > 0) {
      const top = this.heap[0];
      if (top.cancelled) {
        this.heapPop();
        continue;
      }
      if (top.fireAtMs > this.clock.now()) break;
      due.push(this.heapPop());
    }
    for (const timer of due) {
      if (!timer.cancelled) timer.callback();
    }
  }

  /** Deadline of the next armed timer, or null if none. */
  nextDeadline(): number | null {
    while (this.heap.length > 0 && this.heap[0].cancelled) {
      this.heapPop();
    }
    return this.heap.length > 0 ? this.heap[0].fireAtMs : null;
  }

  private heap: PendingTimer[] = [];

  private heapPush(timer: PendingTimer): void {
    const heap = this.heap;
    heap.push(timer);
    let index = heap.length - 1;
    while (index > 0) {
      const parent = (index - 1) >> 1;
      if (heap[parent].fireAtMs <= heap[index].fireAtMs) break;
      [heap[parent], heap[index]] = [heap[index], heap[parent]];
      index = parent;
    }
  }

  private heapPop(): PendingTimer {
    const heap = this.heap;
    const top = heap[0];
    const last = heap.pop() as PendingTimer;
    if (heap.length > 0) {
      heap[0] = last;
      let index = 0;
      for (;;) {
        const left = index * 2 + 1;
        const right = left + 1;
        let smallest = index;
        if (left < heap.length && heap[left].fireAtMs < heap[smallest].fireAtMs) {
          smallest = left;
        }
        if (
          right < heap.length &&
          heap[right].fireAtMs < heap[smallest].fireAtMs
        ) {
          smallest = right;
        }
        if (smallest === index) break;
        [heap[smallest], heap[index]] = [heap[index], heap[smallest]];
        index = smallest;
      }
    }
    return top;
  }
}
