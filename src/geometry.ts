export interface Point {
  x: number;
  y: number;
}

/**
 * Signed area x2 of triangle (a,b,c).
 * > 0 when c is to the left of directed edge a->b (CCW triangle).
 */
export function orient2d(a: Point, b: Point, c: Point): number {
  return (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x);
}

/**
 * In-circle determinant for CCW triangle (a,b,c) and point p.
 * > 0  => p is strictly inside the circumcircle of (a,b,c)
 * = 0  => p is cocircular
 * < 0  => p is outside
 */
export function inCircle(a: Point, b: Point, c: Point, p: Point): number {
  const ax = a.x - p.x;
  const ay = a.y - p.y;
  const bx = b.x - p.x;
  const by = b.y - p.y;
  const cx = c.x - p.x;
  const cy = c.y - p.y;
  const ab = ax * by - ay * bx;
  const bc = bx * cy - by * cx;
  const ca = cx * ay - cy * ax;
  return (
    (ax * ax + ay * ay) * bc +
    (bx * bx + by * by) * ca +
    (cx * cx + cy * cy) * ab
  );
}

/**
 * Magnitude bound for the inCircle determinant terms, used to build a
 * scale-invariant significance threshold: |det| below ~1e-12 of this
 * bound is numerically indistinguishable from cocircular.
 */
export function inCircleBound(a: Point, b: Point, c: Point, p: Point): number {
  const ax = a.x - p.x;
  const ay = a.y - p.y;
  const bx = b.x - p.x;
  const by = b.y - p.y;
  const cx = c.x - p.x;
  const cy = c.y - p.y;
  const ab = ax * by - ay * bx;
  const bc = bx * cy - by * cx;
  const ca = cx * ay - cy * ax;
  return (
    (ax * ax + ay * ay) * Math.abs(bc) +
    (bx * bx + by * by) * Math.abs(ca) +
    (cx * cx + cy * cy) * Math.abs(ab)
  );
}

export function circumcenter(a: Point, b: Point, c: Point): Point {
  const d = 2 * (a.x * (b.y - c.y) + b.x * (c.y - a.y) + c.x * (a.y - b.y));
  const aa = a.x * a.x + a.y * a.y;
  const bb = b.x * b.x + b.y * b.y;
  const cc = c.x * c.x + c.y * c.y;
  return {
    x: (aa * (b.y - c.y) + bb * (c.y - a.y) + cc * (a.y - b.y)) / d,
    y: (aa * (c.x - b.x) + bb * (a.x - c.x) + cc * (b.x - a.x)) / d,
  };
}

export function dist2(a: Point, b: Point): number {
  const dx = a.x - b.x;
  const dy = a.y - b.y;
  return dx * dx + dy * dy;
}
