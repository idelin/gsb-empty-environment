/**
 * FIFO async mutex. Used to serialize WAL appends and to guarantee that
 * recovery runs exclusively (concurrent recover() calls are queued, and the
 * second run is a harmless no-op thanks to LSN idempotency).
 */
export class Mutex {
  private tail: Promise<void> = Promise.resolve();
  private waiters = 0;
  private held = false;

  get isLocked(): boolean {
    return this.held;
  }

  get pending(): number {
    return this.waiters;
  }

  async acquire(): Promise<() => void> {
    this.waiters += 1;
    const previous = this.tail;
    let release!: () => void;
    this.tail = new Promise<void>((resolve) => {
      release = resolve;
    });
    await previous;
    this.waiters -= 1;
    this.held = true;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      this.held = false;
      release();
    };
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
