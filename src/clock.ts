/** Deterministic logical clock. Tests advance time manually to control lease expiry. */
export class ManualClock {
  private t = 0;
  now(): number {
    return this.t;
  }
  advance(ms: number): void {
    if (ms < 0) throw new Error('cannot move clock backwards');
    this.t += ms;
  }
}
