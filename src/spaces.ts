import type { Mat3, Vec3 } from './types.ts';
import { IDENTITY } from './matrix.ts';
import { LINEAR, SRGB } from './transfer.ts';
import type { TransferFunction } from './transfer.ts';

/**
 * A color space node: an explicit white point, a transfer function and
 * the matrix mapping its *linear* RGB to XYZ (identity for XYZ spaces).
 */
export interface ColorSpace {
  id: string;
  /** Explicit white point as XYZ tristimulus (Y = 1). */
  white: Vec3;
  transfer: TransferFunction;
  /** Linear RGB -> XYZ, valid under this space's own white point. */
  rgbToXYZ: Mat3;
}

export const D65: Vec3 = [0.95047, 1.0, 1.08883];
export const D50: Vec3 = [0.96422, 1.0, 1.8251];

/** sRGB primaries -> XYZ (D65), IEC 61966-2-1. */
export const SRGB_TO_XYZ: Mat3 = [
  0.4123908, 0.3575843, 0.1804808,
  0.2126390, 0.7151687, 0.0721923,
  0.0193308, 0.1191948, 0.9505322,
];

export function srgbSpace(): ColorSpace {
  return { id: 'srgb', white: [...D65], transfer: SRGB, rgbToXYZ: [...SRGB_TO_XYZ] };
}

export function linearSrgbSpace(): ColorSpace {
  return { id: 'linear-srgb', white: [...D65], transfer: LINEAR, rgbToXYZ: [...SRGB_TO_XYZ] };
}

export function xyzSpace(white: Vec3, id?: string): ColorSpace {
  const tag = id ?? `xyz(${white.map((c) => c.toFixed(5)).join(',')})`;
  return { id: tag, white: [...white], transfer: LINEAR, rgbToXYZ: [...IDENTITY] };
}
