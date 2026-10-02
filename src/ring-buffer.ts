/**
 * 定容环形队列（Ring Buffer）。
 *
 * Node.js 单线程事件循环下，head/tail 索引的读写在两次 await 之间天然原子，
 * 因此无需任何锁即可保证 SPSC 语义下的线程/协程安全（lock-free）。
 * 槽位复用、无动态扩容，读写均为 O(1)。
 */
export class RingBuffer<T> {
  private readonly slots: (T | undefined)[];
  private head = 0;
  private tail = 0;
  private count = 0;
  public readonly capacity: number;

  constructor(capacity: number) {
    if (!Number.isInteger(capacity) || capacity < 0) {
      throw new RangeError(`capacity must be a non-negative integer, got ${capacity}`);
    }
    this.capacity = capacity;
    // 容量 0 时仍分配 1 个槽位以避免取模除零；isFull() 会恒为 true。
    this.slots = new Array(Math.max(capacity, 1));
  }

  get size(): number {
    return this.count;
  }

  isEmpty(): boolean {
    return this.count === 0;
  }

  isFull(): boolean {
    return this.count === this.capacity;
  }

  /** 队满时返回 false，否则入队并返回 true。 */
  push(value: T): boolean {
    if (this.isFull()) return false;
    this.slots[this.tail] = value;
    this.tail = (this.tail + 1) % this.slots.length;
    this.count++;
    return true;
  }

  /** 队空时返回 undefined。 */
  pop(): T | undefined {
    if (this.isEmpty()) return undefined;
    const value = this.slots[this.head];
    this.slots[this.head] = undefined; // 释放引用，便于 GC
    this.head = (this.head + 1) % this.slots.length;
    this.count--;
    return value;
  }
}
