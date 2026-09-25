/**
 * Tiled, resumable streaming over the diffusion engine.
 *
 * Rows are always processed in the same global raster order and the
 * diffusion frontier is carried across tile boundaries, so the output is
 * bit-identical for any tile height. A run can be paused after any tile
 * (snapshot the returned state) and resumed later, even in another
 * process, as long as the color space version matches.
 */

import type { ColorSpace } from "./colorspace.ts";
import {
  createState,
  processRow,
  type DiffusionState,
} from "./diffuse.ts";
import { paletteInSpace } from "./quantize.ts";
import type { IndexedImage, Palette, RGBAImage } from "./types.ts";

export interface Tile {
  /** Absolute y of the first row in this tile. */
  y: number;
  height: number;
  /** height * width palette indices, row-major. */
  indices: Int32Array;
}

export interface StreamOptions {
  dither: boolean;
  /** Rows per tile; must be >= 1. Does not affect the output. */
  tileHeight: number;
  /** Resume from a previous frontier; defaults to a fresh state. */
  state?: DiffusionState;
}

/**
 * Quantize `image` against `palette`, yielding one tile at a time.
 * The generator's return value is the frontier state after the last
 * processed row (snapshot it to resume a partially consumed stream).
 */
export function* quantizeTiled(
  image: RGBAImage,
  palette: Palette,
  space: ColorSpace,
  opts: StreamOptions,
): Generator<Tile, DiffusionState, void> {
  if (!Number.isInteger(opts.tileHeight) || opts.tileHeight < 1) {
    throw new Error(`tileHeight must be an integer >= 1, got ${opts.tileHeight}`);
  }
  const state = opts.state ?? createState(image.width, space.version);
  if (state.width !== image.width) {
    throw new Error("frontier width does not match image width");
  }
  if (state.colorSpaceVersion !== space.version) {
    throw new Error(
      `color space mismatch: frontier=${state.colorSpaceVersion} requested=${space.version}`,
    );
  }
  const ps = paletteInSpace(palette, space);

  let y = state.nextRow;
  while (y < image.height) {
    const height = Math.min(opts.tileHeight, image.height - y);
    const indices = new Int32Array(height * image.width);
    const rowBuf = new Int32Array(image.width);
    for (let r = 0; r < height; r++) {
      processRow(image, y + r, palette, ps, space, opts.dither, state, rowBuf);
      indices.set(rowBuf, r * image.width);
    }
    yield { y, height, indices };
    y += height;
  }
  return state;
}

/** Convenience: run the whole image and assemble the indexed output. */
export function quantizeFull(
  image: RGBAImage,
  palette: Palette,
  space: ColorSpace,
  opts: { dither: boolean; tileHeight?: number; state?: DiffusionState },
): { indexed: IndexedImage; state: DiffusionState } {
  const indices = new Int32Array(image.width * image.height);
  const gen = quantizeTiled(image, palette, space, {
    dither: opts.dither,
    tileHeight: opts.tileHeight ?? image.height,
    state: opts.state,
  });
  let state: DiffusionState | undefined;
  for (;;) {
    const step = gen.next();
    if (step.done) {
      state = step.value;
      break;
    }
    const tile = step.value;
    indices.set(tile.indices, tile.y * image.width);
  }
  return {
    indexed: { width: image.width, height: image.height, indices },
    state: state!,
  };
}
