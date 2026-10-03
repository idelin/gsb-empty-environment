import { AtomicCell } from '../atomic.ts';

interface Node<T> {
  value: T | null;
  next: AtomicCell<Node<T> | null>;
}

/**
 * Michael & Scott 无锁并发队列（MPMC，无界）。
 *
 * 入队/出队仅通过 AtomicCell.compareExchange 推进头尾指针，
 * 没有任何互斥锁；竞争失败时按经典算法帮助（helping）移动尾指针后重试。
 * 出队节点会立即断开 value 引用，保证无对象泄漏。
 */
export class LockFreeQueue<T> {
  #head: AtomicCell<Node<T>>;
  #tail: AtomicCell<Node<T>>;
  #count = new AtomicCell(0);

  constructor() {
    const sentinel: Node<T> = { value: null, next: new AtomicCell(null) };
    this.#head = new AtomicCell(sentinel);
    this.#tail = new AtomicCell(sentinel);
  }

  get size(): number {
    return this.#count.load();
  }

  get isEmpty(): boolean {
    return this.#count.load() === 0;
  }

  enqueue(value: T): void {
    const node: Node<T> = { value, next: new AtomicCell(null) };
    for (;;) {
      const tail = this.#tail.load();
      const next = tail.next.load();
      if (tail !== this.#tail.load()) continue; // 尾指针已移动，重读
      if (next !== null) {
        // 尾指针落后：帮助推进后再试
        this.#tail.compareExchange(tail, next);
        continue;
      }
      if (Object.is(tail.next.compareExchange(null, node), null)) {
        // 链接成功；尽力推进尾指针（失败也无妨，他人会帮助）
        this.#tail.compareExchange(tail, node);
        this.#count.update((n) => n + 1);
        return;
      }
    }
  }

  dequeue(): T | undefined {
    for (;;) {
      const head = this.#head.load();
      const tail = this.#tail.load();
      const next = head.next.load();
      if (head !== this.#head.load()) continue;
      if (head === tail) {
        if (next === null) return undefined; // 空队列
        // 尾指针落后：帮助推进
        this.#tail.compareExchange(tail, next);
        continue;
      }
      if (next === null) continue; // 状态不一致瞬间，重试
      const value = next.value as T;
      if (Object.is(this.#head.compareExchange(head, next), head)) {
        next.value = null; // 断开引用，避免内存泄漏
        this.#count.update((n) => n - 1);
        return value;
      }
    }
  }

  /** 测试/调试用：队列静默时头尾指针必须重合且尾节点无后继。 */
  isQuiescent(): boolean {
    const head = this.#head.load();
    return head === this.#tail.load() && head.next.load() === null && head.value === null;
  }
}
