/**
 * 原子操作原语层。
 *
 * AtomicCell：基于 CAS 的原子单元。在单线程事件循环中每次方法调用都是
 * 不可中断的原子片段，因此 compareExchange / update 的 CAS 语义与多线程
 * 硬件 CAS 完全一致；所有上层无锁算法（Michael-Scott 队列、选票更新、
 * 统计计数器）都建立在 compareExchange 之上，杜绝任何粗粒度互斥锁。
 *
 * SharedAtomicCounter：SharedArrayBuffer + Atomics 实现的跨 worker
 * 真并发原子计数器，用于多线程压测与统计。
 */
export class AtomicCell<T> {
  #value: T;

  constructor(initial: T) {
    this.#value = initial;
  }

  load(): T {
    return this.#value;
  }

  store(next: T): void {
    this.#value = next;
  }

  /**
   * 当且仅当当前值与 expected 相同（Object.is）时替换为 next。
   * 返回替换前的旧值；调用方通过比较返回值与 expected 判断 CAS 是否成功。
   */
  compareExchange(expected: T, next: T): T {
    const current = this.#value;
    if (Object.is(current, expected)) {
      this.#value = next;
    }
    return current;
  }

  /** 经典 CAS 重试循环：反复读取-计算-比较交换，直到成功。 */
  update(fn: (current: T) => T): T {
    for (;;) {
      const current = this.#value;
      const next = fn(current);
      if (Object.is(this.compareExchange(current, next), current)) {
        return next;
      }
    }
  }
}

/** 跨 worker 线程的共享原子计数器（32 位有符号）。 */
export class SharedAtomicCounter {
  #view: Int32Array;

  constructor(buffer?: SharedArrayBuffer) {
    this.#view = new Int32Array(buffer ?? new SharedArrayBuffer(4));
  }

  get buffer(): SharedArrayBuffer {
    return this.#view.buffer as SharedArrayBuffer;
  }

  load(): number {
    return Atomics.load(this.#view, 0);
  }

  add(delta: number): number {
    return Atomics.add(this.#view, 0, delta);
  }

  compareExchange(expected: number, next: number): number {
    return Atomics.compareExchange(this.#view, 0, expected, next);
  }
}
