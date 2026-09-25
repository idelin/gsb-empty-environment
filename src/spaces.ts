import { mat3Apply, mat3Identity, mat3Invert, type Mat3 } from './matrix.ts';
import { gammaTransfer, linearTransfer, srgbTransfer, type TransferFunction } from './transfer.ts';
import { D50, D65, whiteToXYZ, type WhitePoint } from './adapt.ts';

export interface Primaries {
  r: [number, number];
  g: [number, number];
  b: [number, number];
}

export interface ColorSpaceDef {
  readonly id: string;
  readonly kind: 'rgb' | 'xyz';
  /** null for the XYZ hub space */
  readonly primaries: Primaries | null;
  readonly white: WhitePoint;
  readonly transfer: TransferFunction;
}

/**
 * Derives the linear RGB -> XYZ matrix from primaries and white point:
 * M = P * diag(S) with S = P^-1 * W, where P holds the primary
 * chromaticities as (x/y, 1, (1-x-y)/y) columns.
 * Throws SingularMatrixError for degenerate (collinear) primaries.
 */
export function rgbToXyzMatrix(def: ColorSpaceDef): Mat3 {
  if (def.kind === 'xyz' || def.primaries === null) return mat3Identity();
  const { r, g, b } = def.primaries;
  const p: Mat3 = [
    r[0] / r[1], g[0] / g[1], b[0] / b[1],
    1, 1, 1,
    (1 - r[0] - r[1]) / r[1], (1 - g[0] - g[1]) / g[1], (1 - b[0] - b[1]) / b[1],
  ];
  const s = mat3Apply(mat3Invert(p), whiteToXYZ(def.white));
  return [
    p[0] * s[0], p[1] * s[1], p[2] * s[2],
    p[3] * s[0], p[4] * s[1], p[5] * s[2],
    p[6] * s[0], p[7] * s[1], p[8] * s[2],
  ];
}

/** Linear XYZ -> RGB matrix. Throws SingularMatrixError if not invertible. */
export function xyzToRgbMatrix(def: ColorSpaceDef): Mat3 {
  if (def.kind === 'xyz' || def.primaries === null) return mat3Identity();
  return mat3Invert(rgbToXyzMatrix(def));
}

export const SRGB_PRIMARIES: Primaries = {
  r: [0.64, 0.33],
  g: [0.3, 0.6],
  b: [0.15, 0.06],
};

export const DISPLAY_P3_PRIMARIES: Primaries = {
  r: [0.68, 0.32],
  g: [0.265, 0.69],
  b: [0.15, 0.06],
};

export const PROPHOTO_PRIMARIES: Primaries = {
  r: [0.7347, 0.2653],
  g: [0.1596, 0.8404],
  b: [0.0366, 0.0001],
};

export function srgbSpace(): ColorSpaceDef {
  return { id: 'srgb', kind: 'rgb', primaries: SRGB_PRIMARIES, white: D65, transfer: srgbTransfer };
}

export function srgbLinearSpace(): ColorSpaceDef {
  return { id: 'srgb-linear', kind: 'rgb', primaries: SRGB_PRIMARIES, white: D65, transfer: linearTransfer };
}

export function displayP3Space(): ColorSpaceDef {
  return { id: 'display-p3', kind: 'rgb', primaries: DISPLAY_P3_PRIMARIES, white: D65, transfer: srgbTransfer };
}

/** Wide-gamut D50 space; forces real chromatic adaptation from/to D65 spaces. */
export function prophotoSpace(): ColorSpaceDef {
  return { id: 'prophoto', kind: 'rgb', primaries: PROPHOTO_PRIMARIES, white: D50, transfer: gammaTransfer(1.8, 'gamma-1.8') };
}

export function xyzSpace(): ColorSpaceDef {
  return { id: 'xyz', kind: 'xyz', primaries: null, white: D65, transfer: linearTransfer };
}

export function builtinSpaces(): ColorSpaceDef[] {
  return [srgbSpace(), srgbLinearSpace(), displayP3Space(), prophotoSpace(), xyzSpace()];
}
