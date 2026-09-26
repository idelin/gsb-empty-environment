import type { RgbaImage, RgbaPixel } from "./types.ts";

/**
 * Alpha at or below this threshold is treated as fully transparent,
 * anything above as fully opaque. The decision is binary so that every
 * transparent pixel is represented by a single sentinel index and never
 * consumes an opaque palette slot.
 */
export const TRANSPARENT_ALPHA_THRESHOLD = 128;

/** Current color-space / diffusion algorithm version. */
export const COLOR_SPACE_VERSION = "srgb-rgb-fs-v1";

export function isOpaqueAlpha(alpha: number): boolean {
  return alpha > TRANSPARENT_ALPHA_THRESHOLD;
}

export function isOpaqueAt(image: RgbaImage, x: number, y: number): boolean {
  return isOpaqueAlpha(image.data[(y * image.width + x) * 4 + 3]!);
}

/** Build an RGBA image from a compact pixel descriptor array. */
export function makeImage(width: number, height: number, pixels: RgbaPixel[]): RgbaImage {
  if (pixels.length !== width * height) {
    throw new Error(`expected ${width * height} pixels, got ${pixels.length}`);
  }
  const data = new Uint8Array(width * height * 4);
  for (let i = 0; i < pixels.length; i++) {
    const p = pixels[i]!;
    data[i * 4] = p[0];
    data[i * 4 + 1] = p[1];
    data[i * 4 + 2] = p[2];
    data[i * 4 + 3] = p[3];
  }
  return { width, height, data };
}

export function getPixel(image: RgbaImage, x: number, y: number): RgbaPixel {
  const o = (y * image.width + x) * 4;
  return [
    image.data[o]!,
    image.data[o + 1]!,
    image.data[o + 2]!,
    image.data[o + 3]!,
  ];
}

export function setPixel(image: RgbaImage, x: number, y: number, rgba: RgbaPixel): void {
  const o = (y * image.width + x) * 4;
  image.data[o] = rgba[0];
  image.data[o + 1] = rgba[1];
  image.data[o + 2] = rgba[2];
  image.data[o + 3] = rgba[3];
}

export function cloneImage(image: RgbaImage): RgbaImage {
  return { width: image.width, height: image.height, data: new Uint8Array(image.data) };
}

/** Precompute the binary opacity mask. */
export function opacityMask(image: RgbaImage): Uint8Array {
  const n = image.width * image.height;
  const mask = new Uint8Array(n);
  for (let i = 0; i < n; i++) {
    mask[i] = isOpaqueAlpha(image.data[i * 4 + 3]!) ? 1 : 0;
  }
  return mask;
}

/**
 * Extract distinct opaque colors with integer weights (pixel counts).
 * Transparent pixels are ignored entirely.
 */
export function distinctOpaqueColors(image: RgbaImage): {
  color: [number, number, number];
  weight: number;
}[] {
  const seen = new Map<number, { color: [number, number, number]; weight: number }>();
  const n = image.width * image.height;
  for (let i = 0; i < n; i++) {
    const o = i * 4;
    if (!isOpaqueAlpha(image.data[o + 3]!)) continue;
    const r = image.data[o]!;
    const g = image.data[o + 1]!;
    const b = image.data[o + 2]!;
    const key = (r << 16) | (g << 8) | b;
    const existing = seen.get(key);
    if (existing) {
      existing.weight++;
    } else {
      seen.set(key, { color: [r, g, b], weight: 1 });
    }
  }
  return [...seen.values()].sort((a, b) => colorKey(a.color) - colorKey(b.color));
}

export function colorKey(c: ArrayLike<number>): number {
  return (c[0]! << 16) | (c[1]! << 8) | c[2]!;
}
