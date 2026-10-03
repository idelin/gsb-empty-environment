/**
 * Lamport logical clock.
 *
 * Every participant (arbiter + lease nodes) owns one instance. The clock
 * gives every recorded event a logical timestamp so the audit trail can
 * establish a partial "happens-before" order even when wall clocks of
 * different nodes are skewed by sub-millisecond amounts.
 */
export class LamportClock {
  private counter: number;

  constructor(initial = 0) {
    this.counter = initial;
  }

  /** Current value without advancing. */
  now(): number {
    return this.counter;
  }

  /** Advance for a local event and return the new timestamp. */
  tick(): number {
    this.counter += 1;
    return this.counter;
  }

  /**
   * Merge on message receive: clock = max(local, remote) + 1.
   * Returns the new timestamp.
   */
  merge(remote: number): number {
    this.counter = Math.max(this.counter, remote) + 1;
    return this.counter;
  }
}
