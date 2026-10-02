/**
 * 定容环形队列（Ring Buffer）。
 *
 * 在 Node.js 单线程事件循环模型下，所有入队/出队操作天然原子，
 * 无需互斥锁即可保证线程（协程）安全，即“无锁”语义：
 * 任意一次 push/shift 都不会被其他协程的执行交错打断。
 */
export class RingBuffer<T> {
  private readonly slots: (T | undefined)[];
  private head = 0;
  private tail = 0;
  private count = 0;
  readonly capacity: number;

  constructor(capacity: number) {
    if (!Number.isInteger(capacity) || capacity < 0) {
      throw new RangeError(`capacity must be a non-negative integer, got ${capacity}`);
    }
    this.capacity = capacity;
    this.slots = new Array<T | undefined>(capacity);
  }

  get size(): number {
    return this.count;
  }

  get isEmpty(): boolean {
    return this.count === 0;
  }

  get isFull(): boolean {
    return this.count === this.capacity;
  }

  /** 入队；队列满时返回 false（不覆盖、不扩容）。 */
  push(value: T): boolean {
    if (this.isFull) return false;
    this.slots[this.tail] = value;
    this.tail = (this.tail + 1) % this.capacity;
    this.count += 1;
    return true;
  }

  /** 出队；队列空时返回 undefined。 */
  shift(): T | undefined {
    if (this.isEmpty) return undefined;
    const value = this.slots[this.head];
    this.slots[this.head] = undefined;
    this.head = (this.head + 1) % this.capacity;
    this.count -= 1;
    return value;
  }
}
