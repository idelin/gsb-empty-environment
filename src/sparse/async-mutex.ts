/**
 * 异步互斥锁：保证并发 async 事务的读-改-写原子性。
 * 公平 FIFO，无忙等待。
 */
export class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();

  async acquire(): Promise<() => void> {
    let release!: () => void;
    const next = new Promise<void>((resolve) => { release = resolve; });
    const prev = this.tail;
    this.tail = this.tail.then(() => next);
    await prev;
    return release;
  }

  async runExclusive<R>(fn: () => R | Promise<R>): Promise<R> {
    const release = await this.acquire();
    try {
      return await fn();
    } finally {
      release();
    }
  }
}
