/** Geographic primitives on an equirectangular local approximation. */

export interface LonLat {
  lon: number;
  lat: number;
}

const EARTH_RADIUS_M = 6371000;
const DEG = Math.PI / 180;

/** Great-circle distance in meters (haversine). */
export function haversine(a: LonLat, b: LonLat): number {
  const dLat = (b.lat - a.lat) * DEG;
  const dLon = (b.lon - a.lon) * DEG;
  const s =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(a.lat * DEG) * Math.cos(b.lat * DEG) * Math.sin(dLon / 2) ** 2;
  return 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(s)));
}

export interface Projection {
  /** Snapped point on the segment. */
  point: LonLat;
  /** Perpendicular distance from the query point to the segment, meters. */
  distance: number;
  /** Fraction along the segment in [0, 1] (0 = start, 1 = end). */
  fraction: number;
}

/**
 * Project a point onto a segment using a local equirectangular
 * approximation centered at the query point (accurate for city scales).
 */
export function projectToSegment(p: LonLat, a: LonLat, b: LonLat): Projection {
  const cosLat = Math.cos(p.lat * DEG);
  const ax = (a.lon - p.lon) * DEG * EARTH_RADIUS_M * cosLat;
  const ay = (a.lat - p.lat) * DEG * EARTH_RADIUS_M;
  const bx = (b.lon - p.lon) * DEG * EARTH_RADIUS_M * cosLat;
  const by = (b.lat - p.lat) * DEG * EARTH_RADIUS_M;
  const dx = bx - ax;
  const dy = by - ay;
  const lenSq = dx * dx + dy * dy;
  let t = 0;
  if (lenSq > 0) {
    t = -(ax * dx + ay * dy) / lenSq;
    t = Math.max(0, Math.min(1, t));
  }
  const px = ax + t * dx;
  const py = ay + t * dy;
  return {
    point: {
      lon: p.lon + (px / (EARTH_RADIUS_M * cosLat)) / DEG,
      lat: p.lat + (py / EARTH_RADIUS_M) / DEG,
    },
    distance: Math.hypot(px, py),
    fraction: t,
  };
}

/** Length of a polyline in meters. */
export function polylineLength(points: LonLat[]): number {
  let total = 0;
  for (let i = 1; i < points.length; i++) {
    total += haversine(points[i - 1], points[i]);
  }
  return total;
}

/**
 * Interpolate a point at `offset` meters along a polyline.
 * Returns { point, fraction } where fraction is relative to the whole polyline.
 */
export function interpolateAlong(
  points: LonLat[],
  offset: number,
): { point: LonLat; fraction: number } {
  const total = polylineLength(points);
  if (total === 0) return { point: points[0], fraction: 0 };
  const clamped = Math.max(0, Math.min(total, offset));
  let acc = 0;
  for (let i = 1; i < points.length; i++) {
    const segLen = haversine(points[i - 1], points[i]);
    if (acc + segLen >= clamped && segLen > 0) {
      const t = (clamped - acc) / segLen;
      return {
        point: {
          lon: points[i - 1].lon + t * (points[i].lon - points[i - 1].lon),
          lat: points[i - 1].lat + t * (points[i].lat - points[i - 1].lat),
        },
        fraction: clamped / total,
      };
    }
    acc += segLen;
  }
  return { point: points[points.length - 1], fraction: 1 };
}
