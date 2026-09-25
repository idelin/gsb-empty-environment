import { mat3Apply, mat3Invert, mat3Multiply, type Mat3, type Vec3 } from './matrix.ts';

export interface WhitePoint {
  x: number;
  y: number;
  name: string;
}

export const D65: WhitePoint = { x: 0.3127, y: 0.329, name: 'D65' };
export const D50: WhitePoint = { x: 0.3457, y: 0.3585, name: 'D50' };

export const WHITE_EQUAL_TOLERANCE = 1e-9;

export function whitePointsEqual(a: WhitePoint, b: WhitePoint): boolean {
  return Math.abs(a.x - b.x) <= WHITE_EQUAL_TOLERANCE && Math.abs(a.y - b.y) <= WHITE_EQUAL_TOLERANCE;
}

export function whiteToXYZ(w: WhitePoint): Vec3 {
  return [w.x / w.y, 1, (1 - w.x - w.y) / w.y];
}

/** Bradford cone response matrix. */
export const BRADFORD: Mat3 = [
  0.8951, 0.2664, -0.1614,
  -0.7502, 1.7135, 0.0367,
  0.0389, -0.0685, 1.0296,
];

/**
 * Chromatic adaptation matrix mapping XYZ tristimulus values referenced to
 * `from` white into values referenced to `to` white (Bradford method).
 * Callers must NOT skip this when the white points differ.
 */
export function adaptationMatrix(from: WhitePoint, to: WhitePoint): Mat3 {
  if (whitePointsEqual(from, to)) {
    return [1, 0, 0, 0, 1, 0, 0, 0, 1];
  }
  const bradfordInv = mat3Invert(BRADFORD);
  const src = mat3Apply(BRADFORD, whiteToXYZ(from));
  const dst = mat3Apply(BRADFORD, whiteToXYZ(to));
  const diagonal: Mat3 = [
    dst[0] / src[0], 0, 0,
    0, dst[1] / src[1], 0,
    0, 0, dst[2] / src[2],
  ];
  return mat3Multiply(bradfordInv, mat3Multiply(diagonal, BRADFORD));
}
