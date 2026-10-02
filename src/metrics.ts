import { crossTrackM } from './geo.ts';
import type { TrajectoryPoint } from './types.ts';

/**
 * Maximum perpendicular distance (m) from any raw point to the compressed
 * polyline — the shape-fidelity error that drives map-matching correctness.
 */
export function maxCrossTrackErrorM(
  raw: TrajectoryPoint[],
  kept: TrajectoryPoint[],
): number {
  if (kept.length < 2 || raw.length === 0) return 0;
  let maxErr = 0;
  let seg = 0;
  for (const p of raw) {
    while (seg < kept.length - 2 && p.t > kept[seg + 1].t) seg += 1;
    const err = crossTrackM(p, kept[seg], kept[seg + 1]);
    if (err > maxErr) maxErr = err;
  }
  return maxErr;
}

/**
 * Fraction of raw points whose cross-track error stays within `toleranceM`
 * — a proxy for map-matching correctness (a point within tolerance of the
 * reconstructed polyline snaps to the same road segment).
 */
export function mapMatchAccuracy(
  raw: TrajectoryPoint[],
  kept: TrajectoryPoint[],
  toleranceM: number,
): number {
  if (kept.length < 2 || raw.length === 0) return 1;
  let ok = 0;
  let seg = 0;
  for (const p of raw) {
    while (seg < kept.length - 2 && p.t > kept[seg + 1].t) seg += 1;
    if (crossTrackM(p, kept[seg], kept[seg + 1]) <= toleranceM) ok += 1;
  }
  return ok / raw.length;
}

/** Maximum |heading error| (deg) between two aligned point series. */
export function maxHeadingErrorDeg(
  a: TrajectoryPoint[],
  b: TrajectoryPoint[],
): number {
  let maxErr = 0;
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const d = Math.abs(((a[i].heading - b[i].heading + 540) % 360) - 180);
    if (d > maxErr) maxErr = d;
  }
  return maxErr;
}
