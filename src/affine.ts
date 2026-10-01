export interface Affine {
  a: number;
  b: number;
  c: number;
  d: number;
  e: number;
  f: number;
}

export const IDENTITY: Affine = { a: 1, b: 0, c: 0, d: 1, e: 0, f: 0 };

export function det(m: Affine): number {
  return m.a * m.d - m.b * m.c;
}

export function isSingular(m: Affine, eps = 1e-12): boolean {
  const scale =
    Math.abs(m.a) + Math.abs(m.b) + Math.abs(m.c) + Math.abs(m.d);
  return Math.abs(det(m)) <= eps * Math.max(1, scale * scale);
}

export function invert(m: Affine): Affine {
  if (isSingular(m)) {
    throw new Error('singular affine matrix cannot be inverted');
  }
  const d = det(m);
  return {
    a: m.d / d,
    b: -m.b / d,
    c: -m.c / d,
    d: m.a / d,
    e: (m.c * m.f - m.d * m.e) / d,
    f: (m.b * m.e - m.a * m.f) / d,
  };
}

export function multiply(p: Affine, q: Affine): Affine {
  return {
    a: p.a * q.a + p.c * q.b,
    b: p.b * q.a + p.d * q.b,
    c: p.a * q.c + p.c * q.d,
    d: p.b * q.c + p.d * q.d,
    e: p.a * q.e + p.c * q.f + p.e,
    f: p.b * q.e + p.d * q.f + p.f,
  };
}

export function apply(m: Affine, x: number, y: number): [number, number] {
  return [m.a * x + m.c * y + m.e, m.b * x + m.d * y + m.f];
}

export function translation(tx: number, ty: number): Affine {
  return { a: 1, b: 0, c: 0, d: 1, e: tx, f: ty };
}

export function scaling(sx: number, sy: number): Affine {
  return { a: sx, b: 0, c: 0, d: sy, e: 0, f: 0 };
}

export function rotation(radians: number): Affine {
  const cos = Math.cos(radians);
  const sin = Math.sin(radians);
  return { a: cos, b: sin, c: -sin, d: cos, e: 0, f: 0 };
}
