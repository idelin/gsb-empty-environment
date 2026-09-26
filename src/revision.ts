import { clampChannel, nearestIndex } from "./color.ts";
import {
  canDeliver,
  FS_SCALE,
  W_DOWN,
  W_DOWN_LEFT,
  W_DOWN_RIGHT,
  W_RIGHT,
  type MaskGrid,
} from "./diffusion-core.ts";
import { opacityMask, setPixel } from "./image.ts";
import type { PixelEdit, RevisionResult, Rgb, RgbaImage } from "./types.ts";

export interface FullIndexedImageLike {
  width: number;
  height: number;
  palette: readonly Rgb[];
  indices: Uint16Array;
  transparentIndex: number;
  residual: Int32Array;
}

/**
 * Apply local pixel edits to an already diffused indexed image and recompute
 * ONLY the affected diffusion cone downstream of the edits.
 *
 * The affected region is strictly larger than the edited tiles: error flows
 * to right/down-left/down/down-right through opaque pixels (never across the
 * transparent barrier), so an edit can change later rows and neighboring
 * columns. We compute that cone with one raster forward sweep (every
 * Floyd-Steinberg predecessor precedes its successors in raster order), then
 * recompute each affected pixel in raster order, reusing stored residuals of
 * unaffected predecessors.
 */
export function revisePixels(
  image: RgbaImage,
  current: FullIndexedImageLike,
  edits: PixelEdit[]
): RevisionResult {
  const { width, height } = image;
  const n = width * height;

  // Opacity topology before the edit; must be captured before pixels change.
  const oldMask = opacityMask(image);

  for (const edit of edits) {
    if (edit.x < 0 || edit.y < 0 || edit.x >= width || edit.y >= height) {
      throw new Error(`edit out of bounds: (${edit.x}, ${edit.y})`);
    }
    setPixel(image, edit.x, edit.y, edit.rgba);
  }
  const newMask = opacityMask(image);
  const grid: MaskGrid = { width, height, opaque: newMask };
  const oldGrid: MaskGrid = { width, height, opaque: oldMask };

  const affected = new Uint8Array(n);
  for (const edit of edits) {
    affected[edit.y * width + edit.x] = 1;
  }
  // Also treat any pixel whose opacity changed as a seed even if not in edits
  // (covered above since all edits are seeds; kept explicit for clarity).
  for (let i = 0; i < n; i++) {
    if (oldMask[i] !== newMask[i]) affected[i] = 1;
  }

  // Forward raster sweep over the union of the OLD and NEW topologies: an
  // affected opaque pixel in either topology marks every pixel that could
  // receive its error. This covers both removed deliveries (a pixel turned
  // transparent) and added ones, so the cone is never under-approximated.
  const targetsFor = (x: number, y: number): Array<[number, number]> => [
    [x + 1, y],
    [x - 1, y + 1],
    [x, y + 1],
    [x + 1, y + 1],
  ];
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const i = y * width + x;
      if (!affected[i]) continue;
      for (const [tx, ty] of targetsFor(x, y)) {
        if (oldMask[i] && canDeliver(oldGrid, x, y, tx, ty)) {
          affected[ty * width + tx] = 1;
        }
        if (newMask[i] && canDeliver(grid, x, y, tx, ty)) {
          affected[ty * width + tx] = 1;
        }
      }
    }
  }

  const indices = Uint16Array.from(current.indices);
  const residual = Int32Array.from(current.residual);
  let changedPixels = 0;

  // Recompute affected pixels in raster order, summing incoming error from
  // stored residuals of predecessors (gated by the NEW barrier topology).
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const i = y * width + x;
      if (!affected[i]) continue;

      if (!newMask[i]) {
        if (indices[i] !== current.transparentIndex) changedPixels++;
        indices[i] = current.transparentIndex;
        residual[i * 3] = 0;
        residual[i * 3 + 1] = 0;
        residual[i * 3 + 2] = 0;
        continue;
      }

      const incoming: [number, number, number] = [0, 0, 0];
      addIncoming(current.palette, grid, residual, indices, x, y, incoming);
      // incoming sums weights * residualScaled; divide by 16 when applying.
      const o = i * 4;
      const corrected: [number, number, number] = [0, 0, 0];
      for (let c = 0; c < 3; c++) {
        corrected[c] = clampChannel(image.data[o + c]! + Math.floor(incoming[c]! / FS_SCALE));
      }

      const idx = nearestIndex(corrected[0], corrected[1], corrected[2], current.palette);
      if (indices[i] !== idx) changedPixels++;
      indices[i] = idx;
      const chosen = current.palette[idx]!;
      residual[i * 3] = corrected[0] - chosen[0];
      residual[i * 3 + 1] = corrected[1] - chosen[1];
      residual[i * 3 + 2] = corrected[2] - chosen[2];
    }
  }

  let affectedPixels = 0;
  for (let i = 0; i < n; i++) if (affected[i]) affectedPixels++;

  // Mirror updates into the provided indexed image object so callers can keep
  // using it (its arrays are copied here, so assign back).
  current.indices.set(indices);
  current.residual.set(residual);

  return { indices, residual, changedPixels, affectedPixels };
}

function addIncoming(
  _palette: readonly Rgb[],
  grid: MaskGrid,
  residual: Int32Array,
  _indices: Uint16Array,
  x: number,
  y: number,
  incoming: [number, number, number]
): void {
  // Left neighbor (its right-going 7/16).
  if (x - 1 >= 0 && canDeliver(grid, x - 1, y, x, y)) {
    addResidual(residual, (y * grid.width + (x - 1)) * 3, W_RIGHT, incoming);
  }
  if (y < 1) return;
  // Above-right (its down-left 3/16).
  if (canDeliver(grid, x + 1, y - 1, x, y)) {
    addResidual(residual, ((y - 1) * grid.width + (x + 1)) * 3, W_DOWN_LEFT, incoming);
  }
  // Above (its down 5/16).
  if (canDeliver(grid, x, y - 1, x, y)) {
    addResidual(residual, ((y - 1) * grid.width + x) * 3, W_DOWN, incoming);
  }
  // Above-left (its down-right 1/16).
  if (x - 1 >= 0 && canDeliver(grid, x - 1, y - 1, x, y)) {
    addResidual(residual, ((y - 1) * grid.width + (x - 1)) * 3, W_DOWN_RIGHT, incoming);
  }
}

function addResidual(
  residual: Int32Array,
  offset: number,
  weight: number,
  incoming: [number, number, number]
): void {
  incoming[0] += residual[offset]! * weight;
  incoming[1] += residual[offset + 1]! * weight;
  incoming[2] += residual[offset + 2]! * weight;
}

export type { FullIndexedImageLike as RevisedImage };
