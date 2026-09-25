import type { Mat3, Vec3 } from './types.ts';
import { apply, invert, multiply } from './matrix.ts';

/** Bradford cone response matrix. */
const BRADFORD: Mat3 = [
  0.8951, 0.2664, -0.1614,
  -0.7502, 1.7135, 0.0367,
  0.0389, -0.0685, 1.0296,
];

/** Chromatic adaptation matrix mapping `from` white to `to` white (Bradford). */
export function adaptationMatrix(from: Vec3, to: Vec3): Mat3 {
  const bradfordInv = invert(BRADFORD);
  if (!bradfordInv) throw new Error('Bradford matrix is singular');
  const [r1, g1, b1] = apply(BRADFORD, from);
  const [r2, g2, b2] = apply(BRADFORD, to);
  const scale: Mat3 = [r2 / r1, 0, 0, 0, g2 / g1, 0, 0, 0, b2 / b1];
  return multiply(bradfordInv, multiply(scale, BRADFORD));
}

export function whitesEqual(a: Vec3, b: Vec3, eps = 1e-6): boolean {
  return (
    Math.abs(a[0] - b[0]) <= eps &&
    Math.abs(a[1] - b[1]) <= eps &&
    Math.abs(a[2] - b[2]) <= eps
  );
}
