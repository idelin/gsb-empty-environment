import { makePlane, type BoundaryMode, type Plane } from './types.ts';
import { StructuringElement } from './se.ts';
import { localOpPlane } from './ops.ts';

export interface ReconstructResult {
  result: Plane;
  converged: boolean;
  iterations: number;
}

/**
 * Reference morphological reconstruction by geodesic dilation:
 *   R_0 = marker & mask;  R_{n+1} = dilate(R_n) & mask, until fixed point.
 * Iterates whole-image sweeps; stops at the fixed point or when the
 * iteration budget is exhausted (reported, never silently truncated).
 */
export function reconstructReference(
  marker: Plane,
  mask: Plane,
  se: StructuringElement,
  boundary: BoundaryMode = 'zero',
  maxIterations: number = Number.POSITIVE_INFINITY,
): ReconstructResult {
  const { width, height } = mask;
  let cur = makePlane(width, height);
  for (let i = 0; i < cur.data.length; i++) {
    cur.data[i] = marker.data[i] > 0 && mask.data[i] > 0 ? 1 : 0;
  }
  let iterations = 0;
  while (iterations < maxIterations) {
    const dilated = localOpPlane('dilate', cur, se, boundary);
    let changed = false;
    for (let i = 0; i < dilated.data.length; i++) {
      const v = dilated.data[i] > 0 && mask.data[i] > 0 ? 1 : 0;
      if (v !== cur.data[i]) changed = true;
      dilated.data[i] = v;
    }
    cur = dilated;
    iterations++;
    if (!changed) return { result: cur, converged: true, iterations };
  }
  return { result: cur, converged: false, iterations };
}

/** Directed propagation deltas of a reconstruction SE: offset - origin. */
export function forwardDeltas(se: StructuringElement): Array<readonly [number, number]> {
  return se.offsets.map(([dx, dy]) => [dx - se.origin[0], dy - se.origin[1]] as const);
}

/**
 * Symmetrized neighbour deltas (forward and backward). Used to compute the
 * conservative affected region: a change can only matter to pixels connected
 * to it through the (old ∪ new) mask in either propagation direction.
 */
export function symmetricDeltas(se: StructuringElement): Array<readonly [number, number]> {
  const seen = new Set<string>();
  const out: Array<readonly [number, number]> = [];
  for (const [dx, dy] of forwardDeltas(se)) {
    for (const [ex, ey] of [[dx, dy], [-dx, -dy]] as const) {
      if (ex === 0 && ey === 0) continue;
      const key = ex + ',' + ey;
      if (!seen.has(key)) {
        seen.add(key);
        out.push([ex, ey]);
      }
    }
  }
  return out;
}
