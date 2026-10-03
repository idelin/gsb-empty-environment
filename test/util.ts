/** Deterministic PRNG (mulberry32) so tests and demos are reproducible. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Highly skewed Zipf-like distribution over `n` byte symbols. */
export function skewedProbabilities(n: number, exponent = 1.4): number[] {
  const probs: number[] = [];
  let sum = 0;
  for (let i = 0; i < n; i++) {
    const p = 1 / Math.pow(i + 1, exponent);
    probs.push(p);
    sum += p;
  }
  return probs.map((p) => p / sum);
}

/** Sample `length` bytes from a discrete distribution. */
export function sampleSkewed(
  length: number,
  probs: number[],
  seed = 42,
): Uint8Array {
  const cdf: number[] = [];
  let acc = 0;
  for (const p of probs) {
    acc += p;
    cdf.push(acc);
  }
  const rand = mulberry32(seed);
  const out = new Uint8Array(length);
  for (let i = 0; i < length; i++) {
    const r = rand();
    let lo = 0;
    let hi = cdf.length - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (r <= cdf[mid]) hi = mid;
      else lo = mid + 1;
    }
    out[i] = lo;
  }
  return out;
}

export function histogram(data: Uint8Array): number[] {
  const counts = new Array<number>(256).fill(0);
  for (const b of data) counts[b]++;
  return counts;
}
