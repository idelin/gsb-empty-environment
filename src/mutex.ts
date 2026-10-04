/**
 * Async mutual-exclusion lock serializing critical sections within a node.
 *
 * Critical sections are synchronous functions: the lock is never held across an
 * `await`, which structurally prevents lock-ordering deadlocks between nodes
 * (a node never performs network I/O while holding its own lock).
 */
export class Mutex {
  private tail: Promise<void> = Promise.resolve();

  run<T>(fn: () => T): Promise<T> {
    let release!: () => void;
    const acquired = this.tail.then(() => fn());
    this.tail = acquired.then(
      () => undefined,
      () => undefined,
    );
    return acquired;
  }
}
