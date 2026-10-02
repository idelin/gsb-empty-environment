import type { LatLon, Projection } from './types.ts';

export const EARTH_RADIUS_M = 6371000;

const DEG = Math.PI / 180;

/** 两点间大圆距离（haversine，米）。 */
export function haversineM(a: LatLon, b: LatLon): number {
  const dLat = (b.lat - a.lat) * DEG;
  const dLon = (b.lon - a.lon) * DEG;
  const s1 = Math.sin(dLat / 2);
  const s2 = Math.sin(dLon / 2);
  const h = s1 * s1 + Math.cos(a.lat * DEG) * Math.cos(b.lat * DEG) * s2 * s2;
  return 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(h)));
}

interface Xy {
  x: number;
  y: number;
}

/** 以 origin 为原点的等距圆柱局部投影（米），适用于城市尺度。 */
function toXy(p: LatLon, origin: LatLon): Xy {
  return {
    x: (p.lon - origin.lon) * DEG * Math.cos(origin.lat * DEG) * EARTH_RADIUS_M,
    y: (p.lat - origin.lat) * DEG * EARTH_RADIUS_M,
  };
}

function toLatLon(xy: Xy, origin: LatLon): LatLon {
  return {
    lat: origin.lat + xy.y / EARTH_RADIUS_M / DEG,
    lon: origin.lon + xy.x / (EARTH_RADIUS_M * Math.cos(origin.lat * DEG)) / DEG,
  };
}

/**
 * 将点 p 垂直投影到路段 a-b 上（局部平面近似）。
 * fraction 被截断到 [0,1]，即投影点不会超出路段端点。
 */
export function projectToSegment(p: LatLon, a: LatLon, b: LatLon): Projection {
  const pa = toXy(a, p);
  const pb = toXy(b, p);
  const dx = pb.x - pa.x;
  const dy = pb.y - pa.y;
  const lenSq = dx * dx + dy * dy;
  const lengthM = Math.sqrt(lenSq);

  let t = 0;
  if (lenSq > 0) {
    t = -(pa.x * dx + pa.y * dy) / lenSq;
    t = Math.max(0, Math.min(1, t));
  }
  const proj: Xy = { x: pa.x + t * dx, y: pa.y + t * dy };
  return {
    point: toLatLon(proj, p),
    distanceM: Math.hypot(proj.x, proj.y),
    fraction: t,
    alongM: t * lengthM,
  };
}
