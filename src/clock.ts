export type TimerHandle = number;

interface TimerEvent {
  at: number;
  id: number;
  fn: () => void;
  cancelled: boolean;
}

/**
 * Deterministic virtual clock used to drive the SWIM protocol and the
 * simulated network in tests, without relying on real timers.
 */
export class VirtualClock {
  private nowMs = 0;
  private nextId = 0;
  private queue: TimerEvent[] = [];

  get now(): number {
    return this.nowMs;
  }

  schedule(delayMs: number, fn: () => void): TimerHandle {
    const id = ++this.nextId;
    this.queue.push({ at: this.nowMs + delayMs, id, fn, cancelled: false });
    return id;
  }

  cancel(handle: TimerHandle): void {
    const event = this.queue.find((e) => e.id === handle);
    if (event) event.cancelled = true;
  }

  /** Advance virtual time, firing every event scheduled up to `now + ms`. */
  advance(ms: number): void {
    const until = this.nowMs + ms;
    for (;;) {
      let nextIdx = -1;
      for (let i = 0; i < this.queue.length; i++) {
        const e = this.queue[i];
        if (e.cancelled || e.at > until) continue;
        if (
          nextIdx === -1 ||
          e.at < this.queue[nextIdx].at ||
          (e.at === this.queue[nextIdx].at && e.id < this.queue[nextIdx].id)
        ) {
          nextIdx = i;
        }
      }
      if (nextIdx === -1) break;
      const [event] = this.queue.splice(nextIdx, 1);
      this.nowMs = event.at;
      event.fn();
    }
    this.nowMs = until;
  }

  /** Advance until no scheduled events remain. */
  runUntilIdle(): void {
    for (;;) {
      let nextAt = Infinity;
      for (const e of this.queue) {
        if (!e.cancelled && e.at < nextAt) nextAt = e.at;
      }
      if (nextAt === Infinity) break;
      this.advance(nextAt - this.nowMs);
    }
  }
}
