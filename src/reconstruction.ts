import { nearestIndex } from "./color.ts";
import { TRANSPARENT_ALPHA_THRESHOLD, isOpaqueAlpha } from "./image.ts";
import type { IndexedImage, Rgb, RgbaImage } from "./types.ts";

/**
 * Rebuild an RGBA image from indices. Transparent pixels reconstruct to
 * (0, 0, 0, 0); opaque pixels to their palette color with full opacity.
 */
export function reconstruct(indexed: IndexedImage): RgbaImage {
  const { width, height, palette, indices, transparentIndex } = indexed;
  const data = new Uint8Array(width * height * 4);
  for (let i = 0; i < indices.length; i++) {
    const o = i * 4;
    if (indices[i] === transparentIndex) {
      data[o + 3] = 0;
      continue;
    }
    const c = palette[indices[i]!]!;
    data[o] = c[0];
    data[o + 1] = c[1];
    data[o + 2] = c[2];
    data[o + 3] = 255;
  }
  return { width, height, data };
}

export interface ReconstructionErrorReport {
  totalSquaredError: number;
  opaquePixelCount: number;
  transparentPixelCount: number;
  /** SSE over opaque pixels only (RGB channels). */
  opaqueSse: number;
  /** Mean squared error per opaque pixel across 3 channels. */
  opaqueMse: number;
  /**
   * Number of pixels where source opacity and index opacity disagree
   * (transparent sentinel vs opaque palette index).
   */
  opacityMismatches: number;
  /** Transparent source pixels whose reconstructed alpha leaked (non-zero). */
  transparentLeaks: number;
}

/**
 * Compute reconstruction error against the original RGBA image.
 *
 * Transparent pixels do not add RGB error; they are only checked for
 * alpha leaks. Opaque pixels are compared on their three RGB channels.
 */
export function reconstructionError(
  source: RgbaImage,
  indexed: IndexedImage
): ReconstructionErrorReport {
  if (source.width !== indexed.width || source.height !== indexed.height) {
    throw new Error("source and indexed image dimensions differ");
  }
  const recon = reconstruct(indexed);
  let opaqueSse = 0;
  let opaquePixelCount = 0;
  let transparentPixelCount = 0;
  let opacityMismatches = 0;
  let transparentLeaks = 0;

  const n = source.width * source.height;
  for (let i = 0; i < n; i++) {
    const o = i * 4;
    const srcOpaque = isOpaqueAlpha(source.data[o + 3]!);
    const idxTransparent = indexed.indices[i] === indexed.transparentIndex;

    if (srcOpaque === idxTransparent) {
      opacityMismatches++;
    }

    if (!srcOpaque) {
      transparentPixelCount++;
      if (recon.data[o + 3] !== 0) transparentLeaks++;
      continue;
    }

    opaquePixelCount++;
    const dr = source.data[o]! - recon.data[o]!;
    const dg = source.data[o + 1]! - recon.data[o + 1]!;
    const db = source.data[o + 2]! - recon.data[o + 2]!;
    opaqueSse += dr * dr + dg * dg + db * db;
  }

  return {
    totalSquaredError: opaqueSse,
    opaqueSse,
    opaquePixelCount,
    transparentPixelCount,
    opaqueMse: opaquePixelCount > 0 ? opaqueSse / (opaquePixelCount * 3) : 0,
    opacityMismatches,
    transparentLeaks,
  };
}

/** Verify an opaque index array is a nearest-palette assignment (no diffusion). */
export function isNearestAssignment(source: RgbaImage, palette: readonly Rgb[], indices: Uint16Array): boolean {
  const n = source.width * source.height;
  for (let i = 0; i < n; i++) {
    const o = i * 4;
    if (!isOpaqueAlpha(source.data[o + 3]!)) continue;
    const expected = nearestIndex(source.data[o]!, source.data[o + 1]!, source.data[o + 2]!, palette);
    if (indices[i] !== expected) return false;
  }
  return true;
}

export { TRANSPARENT_ALPHA_THRESHOLD };
