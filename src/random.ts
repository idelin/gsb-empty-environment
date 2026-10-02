/** Deterministic PRNG + long-tail samplers shared by tests and the demo. */

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Standard normal via Box-Muller. */
export function normal(rand: () => number): number {
  let u = 0;
  while (u === 0) {
    u = rand();
  }
  const v = rand();
  return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v);
}

/** Lognormal latency in ms: median ~ medianMs, long tail controlled by sigma. */
export function lognormal(rand: () => number, medianMs: number, sigma: number): number {
  return medianMs * Math.exp(sigma * normal(rand));
}
