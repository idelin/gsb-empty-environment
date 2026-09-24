import type { Version } from '../src/model.ts';

/** Deterministic PRNG (mulberry32) for reproducible fuzz runs. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function randInt(rng: () => number, lo: number, hi: number): number {
  return lo + Math.floor(rng() * (hi - lo + 1));
}

export function pick<T>(rng: () => number, items: T[]): T {
  return items[Math.floor(rng() * items.length)];
}

/**
 * Ground truth from the per-character identity model: index of each
 * from-version char in the to-version, or -1 when its identity is gone.
 */
export function groundTruthForward(from: Version, to: Version): number[] {
  const index = new Map(to.chars.map((c, i) => [c.id, i] as const));
  return from.chars.map((c) => index.get(c.id) ?? -1);
}
