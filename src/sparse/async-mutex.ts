/**
 * 异步互斥锁：为跨 await 的复合操作提供临界区，
 * 用于验证/保障单线程事件循环下的并发交错安全。
 */
export class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();

  async acquire(): Promise<() => void> {
    let release!: () => void;
    const next = new Promise<void>((resolve) => (release = resolve));
    const prev = this.tail;
    this.tail = this.tail.then(() => next);
    await prev;
    return release;
  }

  async runExclusive<T>(fn: () => Promise<T> | T): Promise<T> {
    const release = await this.acquire();
    try {
      return await fn();
    } finally {
      release();
    }
  }
}
