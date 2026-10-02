/**
 * Discrete-event simulator with a virtual clock.
 *
 * - Time advances in integer ticks; nothing runs between events.
 * - Events scheduled for the same tick fire in insertion order (FIFO),
 *   so execution order is fully determined by the program, never by
 *   wall-clock time, timers, or the OS scheduler.
 */
export type EventFn = () => void;

interface ScheduledEvent {
  time: number;
  seq: number;
  fn: EventFn;
}

export class Simulator {
  now: number = 0;
  private seq: number = 0;
  private heap: ScheduledEvent[] = [];
  /** Deterministic, human-readable event log (used for replay hashing). */
  readonly eventLog: string[] = [];

  /** Schedule `fn` to run `delay` ticks from now. */
  schedule(delay: number, fn: EventFn): void {
    this.push({ time: this.now + delay, seq: this.seq++, fn });
  }

  /** Schedule `fn` to run at an absolute tick. */
  scheduleAt(time: number, fn: EventFn): void {
    this.push({ time, seq: this.seq++, fn });
  }

  /** Append a line to the deterministic event log. */
  log(entry: string): void {
    this.eventLog.push(`${this.now} ${entry}`);
  }

  get pending(): number {
    return this.heap.length;
  }

  /** Fire the next event. Returns false when the queue is empty. */
  step(): boolean {
    const ev = this.pop();
    if (!ev) return false;
    this.now = ev.time;
    ev.fn();
    return true;
  }

  /** Advance the virtual clock by `ticks`, firing every event due. */
  run(ticks: number): void {
    const until = this.now + ticks;
    while (this.heap.length > 0 && this.heap[0].time <= until) {
      this.step();
    }
    this.now = until;
  }

  private push(ev: ScheduledEvent): void {
    const heap = this.heap;
    heap.push(ev);
    let i = heap.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.less(heap[i], heap[parent])) {
        [heap[i], heap[parent]] = [heap[parent], heap[i]];
        i = parent;
      } else break;
    }
  }

  private pop(): ScheduledEvent | undefined {
    const heap = this.heap;
    if (heap.length === 0) return undefined;
    const top = heap[0];
    const last = heap.pop()!;
    if (heap.length > 0) {
      heap[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (left < heap.length && this.less(heap[left], heap[smallest])) smallest = left;
        if (right < heap.length && this.less(heap[right], heap[smallest])) smallest = right;
        if (smallest === i) break;
        [heap[i], heap[smallest]] = [heap[smallest], heap[i]];
        i = smallest;
      }
    }
    return top;
  }

  private less(a: ScheduledEvent, b: ScheduledEvent): boolean {
    return a.time < b.time || (a.time === b.time && a.seq < b.seq);
  }
}
