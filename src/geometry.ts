export interface Point {
  x: number;
  y: number;
}

/** Twice the signed area of triangle (a,b,c). Positive when (a,b,c) is counter-clockwise. */
export function orient2d(a: Point, b: Point, c: Point): number {
  return (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
}

/**
 * In-circle determinant, assuming (a,b,c) is counter-clockwise.
 *   > 0  d lies strictly inside the circumcircle of (a,b,c)
 *   = 0  d is cocircular with (a,b,c)
 *   < 0  d lies outside
 */
export function inCircle(a: Point, b: Point, c: Point, d: Point): number {
  const adx = a.x - d.x;
  const ady = a.y - d.y;
  const bdx = b.x - d.x;
  const bdy = b.y - d.y;
  const cdx = c.x - d.x;
  const cdy = c.y - d.y;

  const abdet = adx * bdy - bdx * ady;
  const bcdet = bdx * cdy - cdx * bdy;
  const cadet = cdx * ady - adx * cdy;

  const alift = adx * adx + ady * ady;
  const blift = bdx * bdx + bdy * bdy;
  const clift = cdx * cdx + cdy * cdy;

  return alift * bcdet + blift * cadet + clift * abdet;
}

export interface Circle {
  center: Point;
  radius: number;
}

/** Circumcircle of a non-degenerate triangle (a,b,c). */
export function circumcircle(a: Point, b: Point, c: Point): Circle {
  const d = 2 * (a.x * (b.y - c.y) + b.x * (c.y - a.y) + c.x * (a.y - b.y));
  const a2 = a.x * a.x + a.y * a.y;
  const b2 = b.x * b.x + b.y * b.y;
  const c2 = c.x * c.x + c.y * c.y;
  const ux = (a2 * (b.y - c.y) + b2 * (c.y - a.y) + c2 * (a.y - b.y)) / d;
  const uy = (a2 * (c.x - b.x) + b2 * (a.x - c.x) + c2 * (b.x - a.x)) / d;
  const center = { x: ux, y: uy };
  return { center, radius: Math.hypot(a.x - ux, a.y - uy) };
}

export function dist(a: Point, b: Point): number {
  return Math.hypot(a.x - b.x, a.y - b.y);
}
