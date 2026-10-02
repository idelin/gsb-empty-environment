export interface HeapEvent {
  time: number;
  priority: number;
  seq: number;
}

export class MinHeap<T extends HeapEvent> {
  private items: T[] = [];

  get size(): number {
    return this.items.length;
  }

  private static less(a: HeapEvent, b: HeapEvent): boolean {
    if (a.time !== b.time) return a.time < b.time;
    if (a.priority !== b.priority) return a.priority < b.priority;
    return a.seq < b.seq;
  }

  push(item: T): void {
    const items = this.items;
    items.push(item);
    let i = items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (!MinHeap.less(items[i], items[parent])) break;
      [items[i], items[parent]] = [items[parent], items[i]];
      i = parent;
    }
  }

  pop(): T | undefined {
    const items = this.items;
    const n = items.length;
    if (n === 0) return undefined;
    const top = items[0];
    const last = items.pop() as T;
    if (n > 1) {
      items[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (left < items.length && MinHeap.less(items[left], items[smallest])) smallest = left;
        if (right < items.length && MinHeap.less(items[right], items[smallest])) smallest = right;
        if (smallest === i) break;
        [items[i], items[smallest]] = [items[smallest], items[i]];
        i = smallest;
      }
    }
    return top;
  }

  peek(): T | undefined {
    return this.items[0];
  }

  clear(): void {
    this.items.length = 0;
  }
}
