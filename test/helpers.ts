/** Deterministic PRNG + series generator for the test suite. */

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

export interface Generated {
  times: number[];
  values: (number | null)[];
}

/**
 * Random non-decreasing timestamps (with duplicate runs) and values
 * containing spikes, missing gaps and single-point segments.
 */
export function makeSeries(rng: () => number, n: number): Generated {
  const times: number[] = [];
  const values: (number | null)[] = [];
  let t = 0;
  let v = 0;
  for (let i = 0; i < n; i++) {
    // Occasionally repeat the previous timestamp (duplicate times).
    t += rng() < 0.12 ? 0 : Math.floor(rng() * 4);
    times.push(t);
    const r = rng();
    if (r < 0.1) {
      values.push(null); // missing: may open/extend a gap
    } else if (r < 0.16) {
      values.push((rng() - 0.5) * 1e6); // spike
      v = 0;
    } else {
      v += (rng() - 0.5) * 10;
      values.push(v);
    }
  }
  // Sprinkle isolated valid points inside gaps (single-point segments).
  for (let i = 1; i + 1 < n; i++) {
    if (values[i - 1] === null && values[i] === null && values[i + 1] === null && rng() < 0.08) {
      values[i] = (rng() - 0.5) * 100;
    }
  }
  return { times, values };
}

export function randomViewport(
  rng: () => number,
  times: number[],
): { t0: number; t1: number; width: number } {
  const min = times[0];
  const max = times[times.length - 1];
  const span = Math.max(1, max - min);
  const t0 = min + Math.floor(rng() * span * 0.7);
  const t1 = t0 + 1 + Math.floor(rng() * (min + span - t0));
  const width = 1 + Math.floor(rng() * 64);
  return { t0, t1, width };
}
