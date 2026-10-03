export interface Clock {
  now(): number;
}

/** Deterministic clock for tests and simulations. */
export class ManualClock implements Clock {
  private t = 0;
  now(): number {
    return this.t;
  }
  advance(ms: number): void {
    if (ms < 0) throw new RangeError('cannot advance clock by a negative amount');
    this.t += ms;
  }
}

export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
}
