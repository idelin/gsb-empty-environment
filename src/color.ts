export type ColorSpace = 'srgb' | 'linear';

export function srgbToLinear(c: number): number {
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

export function linearToSrgb(c: number): number {
  return c <= 0.0031308 ? c * 12.92 : 1.055 * Math.pow(c, 1 / 2.4) - 0.055;
}

export function isColorSpace(s: string): s is ColorSpace {
  return s === 'srgb' || s === 'linear';
}

export function transferFn(space: ColorSpace): (c: number) => number {
  if (space === 'srgb') return srgbToLinear;
  if (space === 'linear') return linearToSrgb;
  throw new Error(`unknown color space: ${String(space)}`);
}
