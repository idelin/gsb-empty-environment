export interface Clock {
  now(): number;
}

export class ManualClock implements Clock {
  private t = 0;
  now(): number {
    return this.t;
  }
  advance(ms: number): void {
    this.t += ms;
  }
}

export class SystemClock implements Clock {
  now(): number {
    return Date.now();
  }
}
