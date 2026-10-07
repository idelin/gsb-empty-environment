/**
 * Cyclic barrier: blocks each caller until `parties` callers have arrived,
 * then releases all of them and resets for the next generation.
 */
export class Barrier {
  readonly #parties: number;
  #arrived = 0;
  #waiters: Array<() => void> = [];

  constructor(parties: number) {
    if (!Number.isInteger(parties) || parties < 1) {
      throw new RangeError("parties must be a positive integer");
    }
    this.#parties = parties;
  }

  wait(): Promise<void> {
    this.#arrived += 1;
    if (this.#arrived === this.#parties) {
      this.#arrived = 0;
      const waiters = this.#waiters;
      this.#waiters = [];
      for (const release of waiters) release();
      return Promise.resolve();
    }
    return new Promise<void>((resolve) => {
      this.#waiters.push(resolve);
    });
  }
}

/** One-shot gate: callers block until `open()` is called. */
export class Gate {
  #open = false;
  #waiters: Array<() => void> = [];

  open(): void {
    if (this.#open) return;
    this.#open = true;
    const waiters = this.#waiters;
    this.#waiters = [];
    for (const release of waiters) release();
  }

  wait(): Promise<void> {
    if (this.#open) return Promise.resolve();
    return new Promise<void>((resolve) => {
      this.#waiters.push(resolve);
    });
  }
}

/** Manually resolvable promise, useful as a "has started" signal. */
export class Deferred<T = void> {
  readonly promise: Promise<T>;
  #resolve!: (value: T | PromiseLike<T>) => void;
  #reject!: (reason?: unknown) => void;

  constructor() {
    this.promise = new Promise<T>((resolve, reject) => {
      this.#resolve = resolve;
      this.#reject = reject;
    });
  }

  resolve(value: T | PromiseLike<T>): void {
    this.#resolve(value);
  }

  reject(reason?: unknown): void {
    this.#reject(reason);
  }
}
