/**
 * Controllable scheduler: async work is enqueued instead of fired directly,
 * so tests can enumerate interleavings of acquire / borrow / cancel / close
 * by pumping tasks one at a time. `auto: true` drains via microtasks and
 * behaves like ordinary promise scheduling (used by the demo and defaults).
 */
export class Scheduler {
  #queue: Array<() => Promise<void>> = [];
  #auto: boolean;
  #completed = 0;

  constructor(opts: { auto?: boolean } = {}) {
    this.#auto = opts.auto ?? false;
  }

  get pending(): number {
    return this.#queue.length;
  }

  get completed(): number {
    return this.#completed;
  }

  /** Enqueue `fn`; the returned promise settles with its result. */
  task<T>(fn: () => T | Promise<T>): Promise<T> {
    const promise = new Promise<T>((resolve, reject) => {
      this.#queue.push(async () => {
        try {
          resolve(await fn());
        } catch (err) {
          reject(err);
        } finally {
          this.#completed++;
        }
      });
    });
    if (this.#auto) queueMicrotask(() => void this.runUntilIdle());
    return promise;
  }

  /** Run up to `n` queued tasks (FIFO), awaiting each. Returns how many ran. */
  async pump(n = 1): Promise<number> {
    let ran = 0;
    while (ran < n && this.#queue.length > 0) {
      const next = this.#queue.shift();
      if (!next) break;
      await next();
      ran++;
    }
    return ran;
  }

  /** Drain the queue, including tasks enqueued while draining. */
  async runUntilIdle(): Promise<void> {
    while (this.#queue.length > 0) await this.pump(1);
  }
}
