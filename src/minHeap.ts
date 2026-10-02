/** Binary min-heap used as the expiry priority queue. */

export type Less<T> = (a: T, b: T) => boolean;

export class MinHeap<T> {
  private readonly items: T[] = [];
  private readonly less: Less<T>;

  constructor(less: Less<T>) {
    this.less = less;
  }

  get size(): number {
    return this.items.length;
  }

  peek(): T | undefined {
    return this.items[0];
  }

  push(item: T): void {
    this.items.push(item);
    let child = this.items.length - 1;
    while (child > 0) {
      const parent = (child - 1) >> 1;
      if (!this.less(this.items[child], this.items[parent])) break;
      this.swap(child, parent);
      child = parent;
    }
  }

  pop(): T | undefined {
    const top = this.items[0];
    const last = this.items.pop();
    if (this.items.length > 0 && last !== undefined) {
      this.items[0] = last;
      let parent = 0;
      for (;;) {
        const left = parent * 2 + 1;
        const right = left + 1;
        let smallest = parent;
        if (left < this.items.length && this.less(this.items[left], this.items[smallest])) {
          smallest = left;
        }
        if (right < this.items.length && this.less(this.items[right], this.items[smallest])) {
          smallest = right;
        }
        if (smallest === parent) break;
        this.swap(parent, smallest);
        parent = smallest;
      }
    }
    return top;
  }

  private swap(i: number, j: number): void {
    const tmp = this.items[i];
    this.items[i] = this.items[j];
    this.items[j] = tmp;
  }
}
