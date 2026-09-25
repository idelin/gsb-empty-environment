/** Nearest-palette-color lookup in a given color space. */

import type { ColorSpace } from "./colorspace.ts";
import type { Palette } from "./types.ts";

/** Precomputed palette positions in the working color space. */
export interface PaletteSpace {
  points: Float64Array; // colors.length * 3
}

export function paletteInSpace(palette: Palette, space: ColorSpace): PaletteSpace {
  const points = new Float64Array(palette.colors.length * 3);
  for (let i = 0; i < palette.colors.length; i++) {
    const [r, g, b] = palette.colors[i];
    const p = space.toSpace(r, g, b);
    points[i * 3] = p[0];
    points[i * 3 + 1] = p[1];
    points[i * 3 + 2] = p[2];
  }
  return { points };
}

/**
 * Index of the nearest palette color to `p` (a point in the working space).
 * Ties resolve to the lowest palette index (strict less-than scan).
 */
export function nearestIndex(ps: PaletteSpace, p: readonly number[], space: ColorSpace): number {
  const n = ps.points.length / 3;
  let best = -1;
  let bestD = Infinity;
  const tmp = [0, 0, 0];
  for (let i = 0; i < n; i++) {
    tmp[0] = ps.points[i * 3];
    tmp[1] = ps.points[i * 3 + 1];
    tmp[2] = ps.points[i * 3 + 2];
    const d = space.dist2(p, tmp);
    if (d < bestD) {
      bestD = d;
      best = i;
    }
  }
  return best;
}
