export interface Clock {
  now(): number;
}

export class SystemClock implements Clock {
  now(): number {
    return Math.floor(Date.now() / 1000);
  }
}

/** Deterministic clock used by the demo and tests. */
export class ManualClock implements Clock {
  private current: number;

  constructor(start: number) {
    this.current = start;
  }

  now(): number {
    return this.current;
  }

  set(epochSeconds: number): void {
    this.current = epochSeconds;
  }

  advance(seconds: number): void {
    this.current += seconds;
  }
}
