/**
 * Discrete test clock. Time advances only when `tick()` is called;
 * all timeouts, heartbeats and message deliveries are driven by it.
 */
export class Clock {
  now = 0;
  private listeners: Array<() => void> = [];

  onTick(fn: () => void): void {
    this.listeners.push(fn);
  }

  tick(): void {
    this.now += 1;
    for (const fn of [...this.listeners]) fn();
  }

  run(ticks: number): void {
    for (let i = 0; i < ticks; i++) this.tick();
  }
}
