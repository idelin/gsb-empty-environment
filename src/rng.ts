/**
 * Deterministic PRNG (mulberry32). The entire generator state is a single
 * uint32, so it can be captured inside a snapshot and restored bit-exactly.
 */
export class DeterministicRng {
  private state: number;

  constructor(seed: number) {
    this.state = seed >>> 0;
  }

  /** Advance the generator and return a uint32. */
  nextUint32(): number {
    this.state = (this.state + 0x6d2b79f5) >>> 0;
    let t = this.state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return (t ^ (t >>> 14)) >>> 0;
  }

  /** Uniform integer in [0, bound). */
  nextInt(bound: number): number {
    return this.nextUint32() % bound;
  }

  reseed(seed: number): void {
    this.state = seed >>> 0;
  }

  snapshot(): { state: number } {
    return { state: this.state };
  }

  restore(saved: { state: number }): void {
    this.state = saved.state >>> 0;
  }
}
