import type { TrajectoryPoint } from './types.ts';

export const EARTH_RADIUS_M = 6_371_008.8;

const DEG = Math.PI / 180;

/** Great-circle distance in meters (haversine). */
export function haversineM(lat1: number, lon1: number, lat2: number, lon2: number): number {
  const dLat = (lat2 - lat1) * DEG;
  const dLon = (lon2 - lon1) * DEG;
  const a =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(lat1 * DEG) * Math.cos(lat2 * DEG) * Math.sin(dLon / 2) ** 2;
  return 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(a)));
}

/** Initial bearing from point 1 to point 2, degrees clockwise from north. */
export function bearingDeg(lat1: number, lon1: number, lat2: number, lon2: number): number {
  const phi1 = lat1 * DEG;
  const phi2 = lat2 * DEG;
  const dLon = (lon2 - lon1) * DEG;
  const y = Math.sin(dLon) * Math.cos(phi2);
  const x = Math.cos(phi1) * Math.sin(phi2) - Math.sin(phi1) * Math.cos(phi2) * Math.cos(dLon);
  return (Math.atan2(y, x) / DEG + 360) % 360;
}

/** Signed smallest angular difference a - b, in (-180, 180]. */
export function angularDiffDeg(a: number, b: number): number {
  let d = (a - b) % 360;
  if (d > 180) d -= 360;
  if (d <= -180) d += 360;
  return d;
}

/** Normalize an angle to [0, 360). */
export function normDeg(a: number): number {
  return ((a % 360) + 360) % 360;
}

/**
 * Radius of the circle through three points, in meters.
 * Returns Infinity when the points are (near-)collinear.
 */
export function circumradiusM(
  p1: TrajectoryPoint,
  p2: TrajectoryPoint,
  p3: TrajectoryPoint,
): number {
  const a = haversineM(p2.lat, p2.lon, p3.lat, p3.lon);
  const b = haversineM(p1.lat, p1.lon, p3.lat, p3.lon);
  const c = haversineM(p1.lat, p1.lon, p2.lat, p2.lon);
  const s = (a + b + c) / 2;
  const areaSq = s * (s - a) * (s - b) * (s - c);
  if (areaSq <= 1e-12) return Infinity;
  return (a * b * c) / (4 * Math.sqrt(areaSq));
}

/** Linear interpolation. */
export function lerp(a: number, b: number, u: number): number {
  return a + (b - a) * u;
}

/** Interpolate headings along the shortest arc. */
export function lerpAngleDeg(a: number, b: number, u: number): number {
  return normDeg(a + angularDiffDeg(b, a) * u);
}

/**
 * Local tangent-plane projection (equirectangular) centered on a reference
 * latitude. Accurate enough for cross-track error over short segments.
 */
export function toXY(lat: number, lon: number, latRef: number): { x: number; y: number } {
  return {
    x: lon * DEG * EARTH_RADIUS_M * Math.cos(latRef * DEG),
    y: lat * DEG * EARTH_RADIUS_M,
  };
}

/** Perpendicular distance from point p to segment ab, in meters. */
export function crossTrackM(
  p: TrajectoryPoint,
  a: TrajectoryPoint,
  b: TrajectoryPoint,
): number {
  const latRef = p.lat;
  const pp = toXY(p.lat, p.lon, latRef);
  const pa = toXY(a.lat, a.lon, latRef);
  const pb = toXY(b.lat, b.lon, latRef);
  const dx = pb.x - pa.x;
  const dy = pb.y - pa.y;
  const lenSq = dx * dx + dy * dy;
  if (lenSq === 0) return Math.hypot(pp.x - pa.x, pp.y - pa.y);
  let u = ((pp.x - pa.x) * dx + (pp.y - pa.y) * dy) / lenSq;
  u = Math.max(0, Math.min(1, u));
  return Math.hypot(pp.x - (pa.x + u * dx), pp.y - (pa.y + u * dy));
}
