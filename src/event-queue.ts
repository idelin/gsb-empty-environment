interface Entry<T> {
  time: number;
  seq: number;
  payload: T;
}

/**
 * Discrete-event priority queue (binary heap).
 * Ordering is fully deterministic: earliest time first, ties broken by
 * insertion sequence number.
 */
export class EventQueue<T> {
  private heap: Entry<T>[] = [];
  private counter = 0;

  get size(): number {
    return this.heap.length;
  }

  push(time: number, payload: T): void {
    const heap = this.heap;
    heap.push({ time, seq: this.counter++, payload });
    let i = heap.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (EventQueue.less(heap[i], heap[parent])) {
        [heap[i], heap[parent]] = [heap[parent], heap[i]];
        i = parent;
      } else {
        break;
      }
    }
  }

  peekTime(): number | undefined {
    return this.heap[0]?.time;
  }

  pop(): T | undefined {
    const heap = this.heap;
    if (heap.length === 0) return undefined;
    const top = heap[0];
    const last = heap.pop() as Entry<T>;
    if (heap.length > 0) {
      heap[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let min = i;
        if (left < heap.length && EventQueue.less(heap[left], heap[min])) min = left;
        if (right < heap.length && EventQueue.less(heap[right], heap[min])) min = right;
        if (min === i) break;
        [heap[i], heap[min]] = [heap[min], heap[i]];
        i = min;
      }
    }
    return top.payload;
  }

  private static less<T>(a: Entry<T>, b: Entry<T>): boolean {
    return a.time < b.time || (a.time === b.time && a.seq < b.seq);
  }
}
