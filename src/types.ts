export type Vec3 = [number, number, number];

/** Row-major 3x3 matrix. */
export type Mat3 = [
  number, number, number,
  number, number, number,
  number, number, number,
];

export type GamutStrategy = 'clip' | 'preserve';
