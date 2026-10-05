/** 简单二叉最小堆，用于 Dijkstra 的优先队列。 */
export class MinHeap<T> {
  private items: T[] = [];
  private prios: number[] = [];

  get size(): number {
    return this.items.length;
  }

  peekPriority(): number | undefined {
    return this.prios[0];
  }

  push(item: T, prio: number): void {
    this.items.push(item);
    this.prios.push(prio);
    let i = this.items.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (this.prios[p] <= this.prios[i]) break;
      this.swap(i, p);
      i = p;
    }
  }

  pop(): T | undefined {
    if (this.items.length === 0) return undefined;
    const top = this.items[0];
    const lastItem = this.items.pop()!;
    const lastPrio = this.prios.pop()!;
    if (this.items.length > 0) {
      this.items[0] = lastItem;
      this.prios[0] = lastPrio;
      let i = 0;
      for (;;) {
        const l = i * 2 + 1;
        const r = l + 1;
        let m = i;
        if (l < this.prios.length && this.prios[l] < this.prios[m]) m = l;
        if (r < this.prios.length && this.prios[r] < this.prios[m]) m = r;
        if (m === i) break;
        this.swap(i, m);
        i = m;
      }
    }
    return top;
  }

  private swap(a: number, b: number): void {
    [this.items[a], this.items[b]] = [this.items[b], this.items[a]];
    [this.prios[a], this.prios[b]] = [this.prios[b], this.prios[a]];
  }
}
