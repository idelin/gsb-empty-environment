/** 2D affine matrix: x' = a*x + c*y + e, y' = b*x + d*y + f */
export interface Mat {
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
}

export interface Point {
  x: number;
  y: number;
}

export const DET_EPSILON = 1e-12;

export function identity(): Mat {
  return { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };
}

export function translation(tx: number, ty: number): Mat {
  return { a: 1, b: 0, c: 0, d: 1, e: tx, f: ty };
}

export function rotation(radians: number): Mat {
  const cos = Math.cos(radians);
  const sin = Math.sin(radians);
  return { a: cos, b: sin, c: -sin, d: cos, e: 0, f: 0 };
}

export function scaling(sx: number, sy: number): Mat {
  return { a: sx, b: 0, c: 0, d: sy, e: 0, f: 0 };
}

/** Composition: multiply(m, n) applies n first, then m. */
export function multiply(m: Mat, n: Mat): Mat {
  return {
    a: m.a * n.a + m.c * n.b,
    b: m.b * n.a + m.d * n.b,
    c: m.a * n.c + m.c * n.d,
    d: m.b * n.c + m.d * n.d,
    e: m.a * n.e + m.c * n.f + m.e,
    f: m.b * n.e + m.d * n.f + m.f,
  };
}

export function determinant(m: Mat): number {
  return m.a * m.d - m.b * m.c;
}

export function isInvertible(m: Mat): boolean {
  const det = determinant(m);
  return Number.isFinite(det) && Math.abs(det) > DET_EPSILON;
}

/** Returns the inverse, or null when the matrix is singular. */
export function invert(m: Mat): Mat | null {
  const det = determinant(m);
  if (!Number.isFinite(det) || Math.abs(det) <= DET_EPSILON) return null;
  const inv = 1 / det;
  return {
    a: m.d * inv,
    b: -m.b * inv,
    c: -m.c * inv,
    d: m.a * inv,
    e: (m.c * m.f - m.d * m.e) * inv,
    f: (m.b * m.e - m.a * m.f) * inv,
  };
}

export function apply(m: Mat, p: Point): Point {
  return { x: m.a * p.x + m.c * p.y + m.e, y: m.b * p.x + m.d * p.y + m.f };
}
