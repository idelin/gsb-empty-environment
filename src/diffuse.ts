/**
 * Error diffusion, deliberately separated from palette selection.
 *
 * Floyd-Steinberg weights, fixed left-to-right / top-to-bottom scan:
 *   (x+1, y)   7/16   (into the current-row accumulator)
 *   (x-1, y+1) 3/16   (into the next-row accumulator)
 *   (x,   y+1) 5/16
 *   (x+1, y+1) 1/16
 *
 * Transparent pixels are barriers: they emit no error, and any error that
 * would be deposited into them is dropped. Error therefore never crosses a
 * transparent pixel.
 *
 * The frontier (the two error accumulator rows plus the next row index and
 * the color space version) is fully serializable, so a run can be saved and
 * restored bit-exactly.
 */

import type { ColorSpace } from "./colorspace.ts";
import { getColorSpace } from "./colorspace.ts";
import { nearestIndex, type PaletteSpace } from "./quantize.ts";
import { TRANSPARENT_INDEX, type Palette, type RGBAImage } from "./types.ts";

export const FRONTIER_FORMAT_VERSION = 1;

export interface DiffusionState {
  width: number;
  colorSpaceVersion: string;
  /** Absolute index of the next row to process. */
  nextRow: number;
  /** Error accumulators, width * 3 each, in u8 RGB units. */
  errCur: Float64Array;
  errNext: Float64Array;
}

/** JSON-safe snapshot of a diffusion frontier. */
export interface SerializedFrontier {
  format: number;
  width: number;
  colorSpaceVersion: string;
  nextRow: number;
  errCur: number[];
  errNext: number[];
}

export function createState(width: number, colorSpaceVersion: string): DiffusionState {
  getColorSpace(colorSpaceVersion); // validate eagerly
  return {
    width,
    colorSpaceVersion,
    nextRow: 0,
    errCur: new Float64Array(width * 3),
    errNext: new Float64Array(width * 3),
  };
}

export function snapshotState(state: DiffusionState): SerializedFrontier {
  return {
    format: FRONTIER_FORMAT_VERSION,
    width: state.width,
    colorSpaceVersion: state.colorSpaceVersion,
    nextRow: state.nextRow,
    errCur: Array.from(state.errCur),
    errNext: Array.from(state.errNext),
  };
}

export function restoreState(snap: SerializedFrontier): DiffusionState {
  if (snap.format !== FRONTIER_FORMAT_VERSION) {
    throw new Error(`unsupported frontier format: ${snap.format}`);
  }
  getColorSpace(snap.colorSpaceVersion); // throws on unknown version
  if (snap.errCur.length !== snap.width * 3 || snap.errNext.length !== snap.width * 3) {
    throw new Error("frontier buffer size does not match width");
  }
  return {
    width: snap.width,
    colorSpaceVersion: snap.colorSpaceVersion,
    nextRow: snap.nextRow,
    errCur: Float64Array.from(snap.errCur),
    errNext: Float64Array.from(snap.errNext),
  };
}

function clamp255(v: number): number {
  return v < 0 ? 0 : v > 255 ? 255 : v;
}

/**
 * Process one row of the image, writing palette indices into `out`
 * (length === width). Mutates `state`: consumes errCur, fills errNext,
 * then rotates the buffers and advances nextRow.
 */
export function processRow(
  image: RGBAImage,
  y: number,
  palette: Palette,
  ps: PaletteSpace,
  space: ColorSpace,
  dither: boolean,
  state: DiffusionState,
  out: Int32Array,
): void {
  const { width, data } = image;
  const { errCur, errNext } = state;
  const hasNextRow = y + 1 < image.height;
  const adjusted = [0, 0, 0];

  for (let x = 0; x < width; x++) {
    const i4 = (y * width + x) * 4;
    if (data[i4 + 3] === 0) {
      // Transparent barrier: emit the reserved index and swallow any
      // accumulated error so nothing propagates through this pixel.
      out[x] = TRANSPARENT_INDEX;
      errCur[x * 3] = 0;
      errCur[x * 3 + 1] = 0;
      errCur[x * 3 + 2] = 0;
      continue;
    }

    const r = clamp255(data[i4] + errCur[x * 3]);
    const g = clamp255(data[i4 + 1] + errCur[x * 3 + 1]);
    const b = clamp255(data[i4 + 2] + errCur[x * 3 + 2]);
    adjusted[0] = r;
    adjusted[1] = g;
    adjusted[2] = b;
    const p = space.toSpace(r, g, b);
    const idx = nearestIndex(ps, p, space);
    out[x] = idx;

    if (!dither) continue;

    const pc = palette.colors[idx];
    const er = r - pc[0];
    const eg = g - pc[1];
    const eb = b - pc[2];

    // Right neighbor, same row. If it is transparent it will swallow the
    // deposit when reached, so no barrier check is needed here.
    if (x + 1 < width) {
      errCur[(x + 1) * 3] += (er * 7) / 16;
      errCur[(x + 1) * 3 + 1] += (eg * 7) / 16;
      errCur[(x + 1) * 3 + 2] += (eb * 7) / 16;
    }
    // Next-row neighbors: skip deposits into transparent pixels (barrier).
    if (hasNextRow) {
      const rowBelow = (y + 1) * width * 4;
      if (x - 1 >= 0 && data[rowBelow + (x - 1) * 4 + 3] !== 0) {
        errNext[(x - 1) * 3] += (er * 3) / 16;
        errNext[(x - 1) * 3 + 1] += (eg * 3) / 16;
        errNext[(x - 1) * 3 + 2] += (eb * 3) / 16;
      }
      if (data[rowBelow + x * 4 + 3] !== 0) {
        errNext[x * 3] += (er * 5) / 16;
        errNext[x * 3 + 1] += (eg * 5) / 16;
        errNext[x * 3 + 2] += (eb * 5) / 16;
      }
      if (x + 1 < width && data[rowBelow + (x + 1) * 4 + 3] !== 0) {
        errNext[(x + 1) * 3] += er / 16;
        errNext[(x + 1) * 3 + 1] += eg / 16;
        errNext[(x + 1) * 3 + 2] += eb / 16;
      }
    }
  }

  // Rotate buffers: next row becomes current; recycle the old current as
  // the fresh (zeroed) next.
  errCur.fill(0);
  state.errCur = errNext;
  state.errNext = errCur;
  state.nextRow = y + 1;
}
