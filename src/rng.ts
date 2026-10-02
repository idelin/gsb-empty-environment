/**
 * Seeded PRNG (mulberry32). Same seed => identical stream, forever.
 * This is the ONLY source of randomness in the whole system.
 */
export class Random {
  private state: number;

  constructor(seed: number) {
    this.state = seed >>> 0;
  }

  /** Uniform float in [0, 1). */
  float(): number {
    let a = this.state | 0;
    a = (a + 0x6d2b79f5) | 0;
    this.state = a;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  }

  /** Uniform integer in [0, bound). */
  int(bound: number): number {
    return Math.floor(this.float() * bound);
  }

  pick<T>(items: readonly T[]): T {
    return items[this.int(items.length)];
  }
}
