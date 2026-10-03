/** FIFO 异步互斥锁。 */
export class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();

  async acquire(): Promise<() => void> {
    let release!: () => void;
    const next = new Promise<void>((resolve) => {
      release = resolve;
    });
    const prev = this.tail;
    this.tail = prev.then(() => next);
    await prev;
    return release;
  }
}

export class LockTransaction<K, V> {
  private readonly data: Map<K, V>;

  constructor(data: Map<K, V>) {
    this.data = data;
  }

  get(key: K): V | undefined {
    return this.data.get(key);
  }

  set(key: K, value: V): void {
    this.data.set(key, value);
  }
}

/**
 * 锁方案基线：一把全局互斥锁包住整个事务（含业务延迟），
 * 所有事务严格串行，用于与 OCC 做吞吐对比。
 */
export class LockEngine<K, V> {
  private readonly data = new Map<K, V>();
  private readonly mutex = new AsyncMutex();
  private commits = 0;

  async run<T>(fn: (txn: LockTransaction<K, V>) => T | Promise<T>): Promise<T> {
    const release = await this.mutex.acquire();
    try {
      const result = await fn(new LockTransaction<K, V>(this.data));
      this.commits += 1;
      return result;
    } finally {
      release();
    }
  }

  getCommitted(key: K): V | undefined {
    return this.data.get(key);
  }

  get committedCount(): number {
    return this.commits;
  }
}
