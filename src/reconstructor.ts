import {
  angularDiffDeg,
  circumradiusM,
  lerp,
  lerpAngleDeg,
} from './geo.ts';
import type { TrajectoryPoint } from './types.ts';

/**
 * Reconstruct a trajectory from the retained (kept) points.
 *
 * Position, elevation and speed are linearly interpolated between kept
 * points; heading is interpolated along the shortest arc. At kept
 * timestamps the restored values are exactly the kept samples, so heading
 * angle and turn radius at critical points are zero-distortion.
 */
export function restoreAt(
  kept: TrajectoryPoint[],
  times: number[],
): TrajectoryPoint[] {
  if (kept.length === 0) return [];
  const out: TrajectoryPoint[] = [];
  let seg = 0;
  for (const t of times) {
    while (seg < kept.length - 2 && t > kept[seg + 1].t) seg += 1;
    const a = kept[seg];
    const b = kept[seg + 1];
    if (b === undefined || t <= a.t) {
      out.push({ ...a });
      continue;
    }
    if (t >= b.t) {
      out.push({ ...b });
      continue;
    }
    const u = (t - a.t) / (b.t - a.t);
    out.push({
      t,
      lat: lerp(a.lat, b.lat, u),
      lon: lerp(a.lon, b.lon, u),
      ele: lerp(a.ele, b.ele, u),
      speed: lerp(a.speed, b.speed, u),
      heading: lerpAngleDeg(a.heading, b.heading, u),
    });
  }
  return out;
}

/** Resample the kept trajectory at a uniform interval (ms). */
export function resampleUniform(
  kept: TrajectoryPoint[],
  intervalMs: number,
): TrajectoryPoint[] {
  if (kept.length === 0) return [];
  const times: number[] = [];
  for (let t = kept[0].t; t <= kept[kept.length - 1].t; t += intervalMs) {
    times.push(t);
  }
  return restoreAt(kept, times);
}

/**
 * Turn radius (m) at kept point `index`, from the circumcircle through the
 * neighboring kept points. Because kept coordinates are exact copies of the
 * raw samples, this equals the radius computed on the raw trajectory at the
 * same indices — zero distortion.
 */
export function turnRadiusAt(points: TrajectoryPoint[], index: number): number {
  if (index <= 0 || index >= points.length - 1) return Infinity;
  return circumradiusM(points[index - 1], points[index], points[index + 1]);
}

/** Signed heading change (deg) between two consecutive points. */
export function headingDeltaDeg(a: TrajectoryPoint, b: TrajectoryPoint): number {
  return angularDiffDeg(b.heading, a.heading);
}
