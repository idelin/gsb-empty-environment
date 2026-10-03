export interface ScheduledEvent {
  time: number;
  seq: number;
  type: string;
  payload: unknown;
}

/**
 * Binary min-heap ordered by (time, seq): events at the same logical time
 * fire in strict FIFO schedule order — the backbone of determinism.
 */
export class Scheduler {
  private heap: ScheduledEvent[] = [];
  private nextSeq = 0;

  get size(): number {
    return this.heap.length;
  }

  schedule(delay: number, type: string, payload: unknown, now: number): void {
    if (!Number.isInteger(delay) || delay < 0) {
      throw new RangeError(`delay must be a non-negative integer, got ${delay}`);
    }
    const event: ScheduledEvent = { time: now + delay, seq: this.nextSeq++, type, payload };
    this.heap.push(event);
    this.bubbleUp(this.heap.length - 1);
  }

  peekTime(): number | null {
    return this.heap.length === 0 ? null : this.heap[0].time;
  }

  pop(): ScheduledEvent | undefined {
    const top = this.heap[0];
    if (top === undefined) return undefined;
    const last = this.heap.pop() as ScheduledEvent;
    if (this.heap.length > 0) {
      this.heap[0] = last;
      this.sinkDown(0);
    }
    return top;
  }

  clear(): void {
    this.heap.length = 0;
    this.nextSeq = 0;
  }

  private static less(a: ScheduledEvent, b: ScheduledEvent): boolean {
    return a.time < b.time || (a.time === b.time && a.seq < b.seq);
  }

  private bubbleUp(i: number): void {
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (!Scheduler.less(this.heap[i], this.heap[parent])) break;
      [this.heap[i], this.heap[parent]] = [this.heap[parent], this.heap[i]];
      i = parent;
    }
  }

  private sinkDown(i: number): void {
    const n = this.heap.length;
    for (;;) {
      const l = 2 * i + 1;
      const r = l + 1;
      let smallest = i;
      if (l < n && Scheduler.less(this.heap[l], this.heap[smallest])) smallest = l;
      if (r < n && Scheduler.less(this.heap[r], this.heap[smallest])) smallest = r;
      if (smallest === i) break;
      [this.heap[i], this.heap[smallest]] = [this.heap[smallest], this.heap[i]];
      i = smallest;
    }
  }
}
