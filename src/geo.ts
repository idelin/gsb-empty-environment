import type { TrajectoryPoint } from './types.ts';

const EARTH_RADIUS_M = 6_371_008.8;
const DEG2RAD = Math.PI / 180;

/** 将有符号航向差归一化到 [-180, 180)。 */
export function headingDeltaDeg(from: number, to: number): number {
  let d = (to - from) % 360;
  if (d < -180) d += 360;
  else if (d >= 180) d -= 360;
  return d;
}

export function haversineM(a: TrajectoryPoint, b: TrajectoryPoint): number {
  const dLat = (b.lat - a.lat) * DEG2RAD;
  const dLon = (b.lon - a.lon) * DEG2RAD;
  const lat1 = a.lat * DEG2RAD;
  const lat2 = b.lat * DEG2RAD;
  const h =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(lat1) * Math.cos(lat2) * Math.sin(dLon / 2) ** 2;
  return 2 * EARTH_RADIUS_M * Math.asin(Math.min(1, Math.sqrt(h)));
}

/** 点到弦 ab 的垂直偏差（米），局部等距圆柱投影，车载场景误差可忽略。 */
export function crossTrackM(
  p: TrajectoryPoint,
  a: TrajectoryPoint,
  b: TrajectoryPoint,
): number {
  const cosLat = Math.cos(((a.lat + b.lat) / 2) * DEG2RAD);
  const ax = 0;
  const ay = 0;
  const bx = (b.lon - a.lon) * DEG2RAD * cosLat * EARTH_RADIUS_M;
  const by = (b.lat - a.lat) * DEG2RAD * EARTH_RADIUS_M;
  const px = (p.lon - a.lon) * DEG2RAD * cosLat * EARTH_RADIUS_M;
  const py = (p.lat - a.lat) * DEG2RAD * EARTH_RADIUS_M;
  const dx = bx - ax;
  const dy = by - ay;
  const lenSq = dx * dx + dy * dy;
  if (lenSq === 0) return Math.hypot(px - ax, py - ay);
  return Math.abs(px * dy - py * dx) / Math.sqrt(lenSq);
}

/** 由速度（m/s）与偏航角速度（度/秒）求转弯半径（米）；直线行驶时返回 Infinity。 */
export function turningRadiusM(speedMps: number, yawRateDegS: number): number {
  const omega = Math.abs(yawRateDegS) * DEG2RAD;
  if (omega === 0) return Infinity;
  return speedMps / omega;
}
