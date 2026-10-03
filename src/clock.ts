/** Source of time, injectable so simulations are deterministic. */
export interface Clock {
  now(): number;
}

/** Manually advanced clock used by the cluster harness and tests. */
export class ManualClock implements Clock {
  private current = 0;

  now(): number {
    return this.current;
  }

  advance(ms: number): number {
    if (ms < 0) throw new Error("cannot move time backwards");
    this.current += ms;
    return this.current;
  }
}
