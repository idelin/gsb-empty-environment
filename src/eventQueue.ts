export interface ScheduledEvent<T> {
  time: number;
  seq: number;
  payload: T;
}

export class EventQueue<T> {
  private heap: ScheduledEvent<T>[] = [];
  private counter = 0;

  get size(): number {
    return this.heap.length;
  }

  push(time: number, payload: T): void {
    const event: ScheduledEvent<T> = { time, seq: this.counter++, payload };
    const heap = this.heap;
    heap.push(event);
    let idx = heap.length - 1;
    while (idx > 0) {
      const parent = (idx - 1) >> 1;
      if (this.before(heap[idx], heap[parent])) {
        const tmp = heap[idx];
        heap[idx] = heap[parent];
        heap[parent] = tmp;
        idx = parent;
      } else {
        break;
      }
    }
  }

  pop(): ScheduledEvent<T> | undefined {
    const heap = this.heap;
    if (heap.length === 0) return undefined;
    const top = heap[0];
    const last = heap.pop() as ScheduledEvent<T>;
    if (heap.length > 0) {
      heap[0] = last;
      let idx = 0;
      for (;;) {
        const left = idx * 2 + 1;
        const right = left + 1;
        let smallest = idx;
        if (left < heap.length && this.before(heap[left], heap[smallest])) smallest = left;
        if (right < heap.length && this.before(heap[right], heap[smallest])) smallest = right;
        if (smallest === idx) break;
        const tmp = heap[idx];
        heap[idx] = heap[smallest];
        heap[smallest] = tmp;
        idx = smallest;
      }
    }
    return top;
  }

  private before(a: ScheduledEvent<T>, b: ScheduledEvent<T>): boolean {
    return a.time < b.time || (a.time === b.time && a.seq < b.seq);
  }
}
