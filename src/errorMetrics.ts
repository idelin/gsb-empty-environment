/**
 * Reconstruction error between the source image and an indexed rendering.
 * Transparent pixels are excluded from every statistic.
 */

import type { ColorSpace } from "./colorspace.ts";
import { TRANSPARENT_INDEX, type IndexedImage, type Palette, type RGBAImage } from "./types.ts";

export interface ErrorMetrics {
  opaquePixels: number;
  transparentPixels: number;
  /** Sum of squared distances in the working color space. */
  sse: number;
  /** sse / opaquePixels (0 when there are no opaque pixels). */
  mse: number;
  rmse: number;
  /** Mean per-channel absolute error in u8 RGB units. */
  mae: number;
  /** Largest single-pixel squared distance in the working space. */
  maxError: number;
}

export function reconstructionError(
  image: RGBAImage,
  indexed: IndexedImage,
  palette: Palette,
  space: ColorSpace,
): ErrorMetrics {
  if (indexed.width !== image.width || indexed.height !== image.height) {
    throw new Error("indexed image dimensions do not match source");
  }
  const { width, height, data } = image;
  let opaque = 0;
  let transparent = 0;
  let sse = 0;
  let absSum = 0;
  let maxError = 0;

  for (let p = 0; p < width * height; p++) {
    const i4 = p * 4;
    if (data[i4 + 3] === 0) {
      transparent++;
      continue;
    }
    opaque++;
    const idx = indexed.indices[p];
    if (idx === TRANSPARENT_INDEX) {
      throw new Error(`opaque pixel ${p} was assigned the transparent index`);
    }
    const c = palette.colors[idx];
    const src = space.toSpace(data[i4], data[i4 + 1], data[i4 + 2]);
    const dst = space.toSpace(c[0], c[1], c[2]);
    const d2 = space.dist2(src, dst);
    sse += d2;
    if (d2 > maxError) maxError = d2;
    absSum +=
      Math.abs(data[i4] - c[0]) + Math.abs(data[i4 + 1] - c[1]) + Math.abs(data[i4 + 2] - c[2]);
  }

  const mse = opaque === 0 ? 0 : sse / opaque;
  return {
    opaquePixels: opaque,
    transparentPixels: transparent,
    sse,
    mse,
    rmse: Math.sqrt(mse),
    mae: opaque === 0 ? 0 : absSum / (opaque * 3),
    maxError,
  };
}
