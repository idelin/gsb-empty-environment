/**
 * High-level deterministic quantization pipeline:
 *   palette selection -> tiled streaming quantization -> error reporting
 *   -> exact small-domain baseline and heuristic gap.
 *
 * The heuristic never claims optimality. When the color domain is small
 * enough, the exact minimum assignment error is computed and the gap is
 * reported; when it is not, the baseline is explicitly absent.
 */

import { SRGB_U8_V1, type ColorSpace } from "./colorspace.ts";
import { reconstructionError, type ErrorMetrics } from "./errorMetrics.ts";
import { exactBaseline, type ExactBaseline } from "./exact.ts";
import { buildPalette } from "./palette.ts";
import { nearestIndex, paletteInSpace } from "./quantize.ts";
import { quantizeFull } from "./stream.ts";
import type { IndexedImage, Palette, RGBAImage } from "./types.ts";

export interface QuantizeOptions {
  /** Number of opaque palette colors; integer >= 1. */
  colors: number;
  /** Enable Floyd-Steinberg error diffusion. Default true. */
  dither?: boolean;
  /** Working color space for distances. Default srgb-u8-v1. */
  colorSpace?: ColorSpace;
  /** Rows per streaming tile. Does not affect the result. */
  tileHeight?: number;
  /** Combination budget for the exact baseline; 0 disables it. */
  exactBaselineLimit?: number;
}

export interface BaselineReport extends ExactBaseline {
  /** Assignment error of the heuristic palette (no diffusion). */
  heuristicError: number;
  /** heuristicError - error (>= 0 up to float noise). */
  gap: number;
  /** gap / error; 0 when the exact error is 0 and the gap is 0. */
  gapRelative: number;
  /** Heuristics report a gap; they never claim optimality. */
  claimsOptimal: false;
}

export interface QuantizeReport {
  palette: Palette;
  indexed: IndexedImage;
  error: ErrorMetrics;
  baseline: BaselineReport | null;
  meta: {
    colorSpaceVersion: string;
    colors: number;
    dither: boolean;
    tileHeight: number;
  };
}

/**
 * Total assignment error of a palette: every opaque pixel mapped to its
 * nearest palette color, no diffusion. Comparable to ExactBaseline.error.
 */
export function assignmentError(image: RGBAImage, palette: Palette, space: ColorSpace): number {
  if (palette.colors.length === 0) return 0;
  const ps = paletteInSpace(palette, space);
  const { data } = image;
  let total = 0;
  for (let i = 0; i < data.length; i += 4) {
    if (data[i + 3] === 0) continue;
    const p = space.toSpace(data[i], data[i + 1], data[i + 2]);
    const idx = nearestIndex(ps, p, space);
    const c = palette.colors[idx];
    total += space.dist2(p, space.toSpace(c[0], c[1], c[2]));
  }
  return total;
}

export function quantizeImage(image: RGBAImage, opts: QuantizeOptions): QuantizeReport {
  const space = opts.colorSpace ?? SRGB_U8_V1;
  const dither = opts.dither ?? true;
  const tileHeight = opts.tileHeight ?? Math.max(1, image.height);

  const palette = buildPalette(image, opts.colors);
  const { indexed } = quantizeFull(image, palette, space, { dither, tileHeight });
  const error = reconstructionError(image, indexed, palette, space);

  let baseline: BaselineReport | null = null;
  const limit = opts.exactBaselineLimit ?? 100_000;
  if (limit > 0) {
    const exact = exactBaseline(image, opts.colors, space, limit);
    if (exact) {
      const heuristicError = assignmentError(image, palette, space);
      const gap = heuristicError - exact.error;
      baseline = {
        ...exact,
        heuristicError,
        gap,
        gapRelative: exact.error === 0 ? 0 : gap / exact.error,
        claimsOptimal: false,
      };
    }
  }

  return {
    palette,
    indexed,
    error,
    baseline,
    meta: {
      colorSpaceVersion: space.version,
      colors: opts.colors,
      dither,
      tileHeight,
    },
  };
}
