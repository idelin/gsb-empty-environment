/**
 * Affected-region analysis for local pixel revisions.
 *
 * When pixels are edited, error diffusion means the change can propagate
 * far beyond the edited tile: Floyd-Steinberg pushes error right and down,
 * so the affected set is a downward-opening wedge, clipped by the image
 * bounds and by transparent barriers (error never crosses them).
 *
 * `affectedRegion` computes exactly the pixels whose quantized output may
 * change, as a list of contiguous per-row spans. Recomputing only those
 * spans (with the frontier restored at the first affected row) is
 * sufficient; everything outside is provably unchanged.
 */

import { isTransparent, type RGBAImage } from "./types.ts";

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface RowSpan {
  y: number;
  x0: number;
  x1: number; // inclusive
}

/**
 * Compute the rows/columns that a revision to `rect` can affect.
 * With `dither: false` the answer is exactly `rect` (no propagation).
 */
export function affectedRegion(image: RGBAImage, rect: Rect, dither: boolean): RowSpan[] {
  const { width, height } = image;
  const rx0 = Math.max(0, rect.x);
  const ry0 = Math.max(0, rect.y);
  const rx1 = Math.min(width - 1, rect.x + rect.width - 1);
  const ry1 = Math.min(height - 1, rect.y + rect.height - 1);
  if (rx0 > rx1 || ry0 > ry1) return [];

  if (!dither) {
    const spans: RowSpan[] = [];
    for (let y = ry0; y <= ry1; y++) spans.push({ y, x0: rx0, x1: rx1 });
    return spans;
  }

  const spans: RowSpan[] = [];
  // Seeds for the current row: columns reachable from the edited rect or
  // from error pushed down by the previous row.
  let seeds = new Set<number>();
  for (let x = rx0; x <= rx1; x++) seeds.add(x);

  for (let y = ry0; y < height; y++) {
    if (y > ry1 && seeds.size === 0) break;

    // Rows inside the edited rect are seeded directly, not just the first.
    if (y > ry0 && y <= ry1) {
      for (let x = rx0; x <= rx1; x++) seeds.add(x);
    }

    // Walk the row left to right, extending through contiguous opaque
    // pixels: an affected pixel pushes error into its right neighbor.
    const affected = new Set<number>();
    let extend = false;
    for (let x = 0; x < width; x++) {
      if (isTransparent(image, x, y)) {
        extend = false; // Barrier: nothing crosses this pixel.
        continue;
      }
      if (seeds.has(x)) extend = true;
      if (extend) affected.add(x);
    }
    if (affected.size === 0) {
      seeds = new Set();
      continue;
    }

    // Record contiguous spans for this row.
    let runStart = -1;
    let prev = -2;
    for (const x of affected) {
      if (x !== prev + 1 && runStart >= 0) {
        spans.push({ y, x0: runStart, x1: prev });
        runStart = x;
      } else if (runStart < 0) {
        runStart = x;
      }
      prev = x;
    }
    spans.push({ y, x0: runStart, x1: prev });

    // Next-row seeds: each affected pixel pushes into (x-1, x, x+1) below,
    // unless that target is transparent.
    const next = new Set<number>();
    if (y + 1 < height) {
      for (const x of affected) {
        for (const nx of [x - 1, x, x + 1]) {
          if (nx >= 0 && nx < width && !isTransparent(image, nx, y + 1)) {
            next.add(nx);
          }
        }
      }
    }
    seeds = next;
  }
  return spans;
}
