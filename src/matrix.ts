export type Vec3 = [number, number, number];

export type Mat3 = [
  number, number, number,
  number, number, number,
  number, number, number,
];

export const SINGULAR_TOLERANCE = 1e-12;

export class SingularMatrixError extends Error {
  constructor(message = 'matrix is singular or ill-conditioned') {
    super(message);
    this.name = 'SingularMatrixError';
  }
}

export function mat3Identity(): Mat3 {
  return [1, 0, 0, 0, 1, 0, 0, 0, 1];
}

export function mat3Multiply(a: Mat3, b: Mat3): Mat3 {
  const out = new Array<number>(9).fill(0);
  for (let row = 0; row < 3; row++) {
    for (let col = 0; col < 3; col++) {
      let sum = 0;
      for (let k = 0; k < 3; k++) {
        sum += a[row * 3 + k] * b[k * 3 + col];
      }
      out[row * 3 + col] = sum;
    }
  }
  return out as unknown as Mat3;
}

export function mat3Determinant(m: Mat3): number {
  const [a, b, c, d, e, f, g, h, i] = m;
  return a * (e * i - f * h) - b * (d * i - f * g) + c * (d * h - e * g);
}

export function mat3Invert(m: Mat3): Mat3 {
  const [a, b, c, d, e, f, g, h, i] = m;
  const c11 = e * i - f * h;
  const c12 = f * g - d * i;
  const c13 = d * h - e * g;
  const det = a * c11 + b * c12 + c * c13;
  if (!Number.isFinite(det) || Math.abs(det) < SINGULAR_TOLERANCE) {
    throw new SingularMatrixError(
      `matrix is singular (|det|=${Math.abs(det)} < ${SINGULAR_TOLERANCE})`,
    );
  }
  const inv = 1 / det;
  return [
    c11 * inv,
    (c * h - b * i) * inv,
    (b * f - c * e) * inv,
    c12 * inv,
    (a * i - c * g) * inv,
    (c * d - a * f) * inv,
    c13 * inv,
    (b * g - a * h) * inv,
    (a * e - b * d) * inv,
  ];
}

export function mat3Apply(m: Mat3, v: Vec3): Vec3 {
  return [
    m[0] * v[0] + m[1] * v[1] + m[2] * v[2],
    m[3] * v[0] + m[4] * v[1] + m[5] * v[2],
    m[6] * v[0] + m[7] * v[1] + m[8] * v[2],
  ];
}

export function mat3MaxAbsDiff(a: Mat3, b: Mat3): number {
  let max = 0;
  for (let k = 0; k < 9; k++) {
    const d = Math.abs(a[k] - b[k]);
    if (d > max) max = d;
  }
  return max;
}

export function mat3IsIdentity(m: Mat3, tolerance = 1e-9): boolean {
  return mat3MaxAbsDiff(m, mat3Identity()) <= tolerance;
}

export function mat3Scale(m: Mat3, s: number): Mat3 {
  return m.map((v) => v * s) as unknown as Mat3;
}
