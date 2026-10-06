export interface Point {
  x: number;
  y: number;
}

/**
 * 2D affine matrix stored as:
 *   | a c e |
 *   | b d f |
 *   | 0 0 1 |
 */
export interface Mat2D {
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
}

export const IDENTITY: Mat2D = { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };

/** Composition: `multiply(m, n)` applies `n` first, then `m`. */
export function multiply(m: Mat2D, n: Mat2D): Mat2D {
  return {
    a: m.a * n.a + m.c * n.b,
    b: m.b * n.a + m.d * n.b,
    c: m.a * n.c + m.c * n.d,
    d: m.b * n.c + m.d * n.d,
    e: m.a * n.e + m.c * n.f + m.e,
    f: m.b * n.e + m.d * n.f + m.f,
  };
}

export function determinant(m: Mat2D): number {
  return m.a * m.d - m.b * m.c;
}

export function isSingular(m: Mat2D): boolean {
  const det = determinant(m);
  return det === 0 || !Number.isFinite(det);
}

/** Returns the inverse, or `null` when the matrix is singular. */
export function invert(m: Mat2D): Mat2D | null {
  if (isSingular(m)) return null;
  const det = determinant(m);
  return {
    a: m.d / det,
    b: -m.b / det,
    c: -m.c / det,
    d: m.a / det,
    e: (m.c * m.f - m.d * m.e) / det,
    f: (m.b * m.e - m.a * m.f) / det,
  };
}

export function apply(m: Mat2D, x: number, y: number): Point {
  return { x: m.a * x + m.c * y + m.e, y: m.b * x + m.d * y + m.f };
}

export function translate(tx: number, ty: number): Mat2D {
  return { a: 1, b: 0, c: 0, d: 1, e: tx, f: ty };
}

export function rotate(radians: number): Mat2D {
  const cos = Math.cos(radians);
  const sin = Math.sin(radians);
  return { a: cos, b: sin, c: -sin, d: cos, e: 0, f: 0 };
}

export function scale(sx: number, sy: number): Mat2D {
  return { a: sx, b: 0, c: 0, d: sy, e: 0, f: 0 };
}
