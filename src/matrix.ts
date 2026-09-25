import type { Mat3, Vec3 } from './types.ts';

export const IDENTITY: Mat3 = [1, 0, 0, 0, 1, 0, 0, 0, 1];

export const SINGULAR_EPS = 1e-12;

export function multiply(a: Mat3, b: Mat3): Mat3 {
  const o = new Array<number>(9);
  for (let r = 0; r < 3; r++) {
    for (let c = 0; c < 3; c++) {
      o[r * 3 + c] =
        a[r * 3] * b[c] + a[r * 3 + 1] * b[3 + c] + a[r * 3 + 2] * b[6 + c];
    }
  }
  return o as unknown as Mat3;
}

export function apply(m: Mat3, v: Vec3): Vec3 {
  return [
    m[0] * v[0] + m[1] * v[1] + m[2] * v[2],
    m[3] * v[0] + m[4] * v[1] + m[5] * v[2],
    m[6] * v[0] + m[7] * v[1] + m[8] * v[2],
  ];
}

export function determinant(m: Mat3): number {
  return (
    m[0] * (m[4] * m[8] - m[5] * m[7]) -
    m[1] * (m[3] * m[8] - m[5] * m[6]) +
    m[2] * (m[3] * m[7] - m[4] * m[6])
  );
}

/** Returns null when the matrix is singular (|det| below SINGULAR_EPS). */
export function invert(m: Mat3): Mat3 | null {
  const det = determinant(m);
  if (!Number.isFinite(det) || Math.abs(det) <= SINGULAR_EPS) return null;
  const c00 = m[4] * m[8] - m[5] * m[7];
  const c01 = -(m[3] * m[8] - m[5] * m[6]);
  const c02 = m[3] * m[7] - m[4] * m[6];
  const c10 = -(m[1] * m[8] - m[2] * m[7]);
  const c11 = m[0] * m[8] - m[2] * m[6];
  const c12 = -(m[0] * m[7] - m[1] * m[6]);
  const c20 = m[1] * m[5] - m[2] * m[4];
  const c21 = -(m[0] * m[5] - m[2] * m[3]);
  const c22 = m[0] * m[4] - m[1] * m[3];
  return [
    c00 / det, c10 / det, c20 / det,
    c01 / det, c11 / det, c21 / det,
    c02 / det, c12 / det, c22 / det,
  ];
}

export function maxAbsDiff(a: Mat3, b: Mat3): number {
  let d = 0;
  for (let i = 0; i < 9; i++) d = Math.max(d, Math.abs(a[i] - b[i]));
  return d;
}
