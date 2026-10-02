/**
 * 容量受限的最小堆：用于流式 Top-K 选择。
 * 堆中始终保留“目前最大的 K 个元素”，堆顶为其中最小者。
 */

export interface HeapItem {
  key: number;   // 排序键（权重）
  value: number; // 负载（网格编号）
}

export class BoundedMinHeap {
  private readonly capacity: number;
  private keys: Float64Array;
  private values: Int32Array;
  private size = 0;

  constructor(capacity: number) {
    if (capacity <= 0) throw new Error('capacity must be positive');
    this.capacity = capacity;
    this.keys = new Float64Array(capacity);
    this.values = new Int32Array(capacity);
  }

  get length(): number {
    return this.size;
  }

  /** 当前堆顶（第 K 大）的键；堆未满时返回 -Infinity */
  threshold(): number {
    return this.size < this.capacity ? -Infinity : this.keys[0];
  }

  /** 尝试插入；仅当 key 足以进入 Top-K 时才保留 */
  offer(key: number, value: number): void {
    if (this.size < this.capacity) {
      this.keys[this.size] = key;
      this.values[this.size] = value;
      this.size++;
      this.siftUp(this.size - 1);
    } else if (key > this.keys[0]) {
      this.keys[0] = key;
      this.values[0] = value;
      this.siftDown(0);
    }
  }

  /** 弹出全部元素，按 key 降序返回 */
  drainSortedDesc(): HeapItem[] {
    const items: HeapItem[] = [];
    for (let i = 0; i < this.size; i++) {
      items.push({ key: this.keys[i], value: this.values[i] });
    }
    items.sort((a, b) => b.key - a.key);
    return items;
  }

  private siftUp(index: number): void {
    let i = index;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.keys[parent] <= this.keys[i]) break;
      this.swap(parent, i);
      i = parent;
    }
  }

  private siftDown(index: number): void {
    let i = index;
    for (;;) {
      const left = 2 * i + 1;
      const right = left + 1;
      let smallest = i;
      if (left < this.size && this.keys[left] < this.keys[smallest]) smallest = left;
      if (right < this.size && this.keys[right] < this.keys[smallest]) smallest = right;
      if (smallest === i) break;
      this.swap(smallest, i);
      i = smallest;
    }
  }

  private swap(a: number, b: number): void {
    const k = this.keys[a];
    this.keys[a] = this.keys[b];
    this.keys[b] = k;
    const v = this.values[a];
    this.values[a] = this.values[b];
    this.values[b] = v;
  }
}
