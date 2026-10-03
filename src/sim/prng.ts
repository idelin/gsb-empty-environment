/**
 * mulberry32 — tiny deterministic PRNG. Same seed ⇒ identical stream on
 * every platform (all ops are 32-bit integer math via Math.imul).
 */
export class Prng {
  private state: number;

  constructor(seed: number) {
    this.state = seed >>> 0;
  }

  /** Next float in [0, 1). */
  next(): number {
    this.state = (this.state + 0x6d2b_79f5) >>> 0;
    let t = this.state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  }

  /** Integer in [0, n). */
  nextInt(n: number): number {
    return Math.floor(this.next() * n);
  }

  /** Deterministic snapshot/restore for replay. */
  snapshot(): number {
    return this.state;
  }

  restore(state: number): void {
    this.state = state >>> 0;
  }
}
