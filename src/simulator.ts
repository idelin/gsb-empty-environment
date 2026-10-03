interface Task {
  time: number;
  seq: number;
  run: () => void;
}

function less(a: Task, b: Task): boolean {
  return a.time < b.time || (a.time === b.time && a.seq < b.seq);
}

/** Virtual-time discrete event simulator backed by a binary min-heap. */
export class Simulator {
  now = 0;
  private heap: Task[] = [];
  private seq = 0;

  schedule(delayMs: number, run: () => void): void {
    const task: Task = { time: this.now + Math.max(0, delayMs), seq: this.seq++, run };
    const heap = this.heap;
    heap.push(task);
    let i = heap.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (less(heap[i], heap[parent])) {
        const tmp = heap[i];
        heap[i] = heap[parent];
        heap[parent] = tmp;
        i = parent;
      } else {
        break;
      }
    }
  }

  runUntil(endMs: number): void {
    const heap = this.heap;
    while (heap.length > 0) {
      const top = heap[0];
      if (top.time > endMs) break;
      const last = heap.pop() as Task;
      if (heap.length > 0) {
        heap[0] = last;
        let i = 0;
        for (;;) {
          const left = i * 2 + 1;
          const right = left + 1;
          let smallest = i;
          if (left < heap.length && less(heap[left], heap[smallest])) smallest = left;
          if (right < heap.length && less(heap[right], heap[smallest])) smallest = right;
          if (smallest === i) break;
          const tmp = heap[i];
          heap[i] = heap[smallest];
          heap[smallest] = tmp;
          i = smallest;
        }
      }
      this.now = top.time;
      top.run();
    }
    this.now = endMs;
  }

  get pending(): number {
    return this.heap.length;
  }
}
