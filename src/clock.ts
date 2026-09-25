/** Injectable clock: all time-dependent logic reads time exclusively through this interface. */
export interface Clock {
  now(): number;
}

/** Wall-clock implementation for production use. */
export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
}

/** Deterministic, manually advanced clock for tests and demos. */
export class ManualClock implements Clock {
  #t: number;

  constructor(start = 0) {
    this.#t = start;
  }

  now(): number {
    return this.#t;
  }

  advance(ms: number): number {
    if (ms < 0) throw new RangeError("cannot advance clock by a negative amount");
    this.#t += ms;
    return this.#t;
  }

  set(t: number): void {
    this.#t = t;
  }
}
