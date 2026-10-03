/**
 * Brute-force exact reference: for every cell, scan every obstacle and
 * keep the best source by (squared distance, source x, source y).
 * Obviously correct, O(cells * obstacles) -- used to validate the fast
 * separable transform and the incremental repair.
 */

export interface RefCell {
  dist: number;
  srcX: number;
  srcY: number;
}

export function bruteForceField(
  width: number,
  height: number,
  obstacles: ReadonlyArray<readonly [number, number]>,
): RefCell[] {
  const out: RefCell[] = new Array(width * height);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      let best = Number.POSITIVE_INFINITY;
      let bx = -1, by = -1;
      for (const [ox, oy] of obstacles) {
        const dx = x - ox, dy = y - oy;
        const d = dx * dx + dy * dy;
        if (d < best || (d === best && (ox < bx || (ox === bx && oy < by)))) {
          best = d; bx = ox; by = oy;
        }
      }
      out[y * width + x] = { dist: best, srcX: bx, srcY: by };
    }
  }
  return out;
}

/** Deterministic PRNG (mulberry32) so randomized tests are reproducible. */
export function makeRng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
