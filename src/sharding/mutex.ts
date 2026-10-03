/** Promise-chain async mutex serializing critical sections. */
export class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();
  private pendingCount = 0;

  /** Number of tasks currently running or waiting for the lock. */
  get pending(): number {
    return this.pendingCount;
  }

  async runExclusive<T>(fn: () => T | Promise<T>): Promise<T> {
    this.pendingCount++;
    const run = this.tail.then(() => fn());
    this.tail = run.then(
      () => undefined,
      () => undefined,
    );
    try {
      return await run;
    } finally {
      this.pendingCount--;
    }
  }
}
