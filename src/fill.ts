import type { Ring } from "./contours.ts";
import { createMask, type Mask } from "./mask.ts";

/**
 * Rasterize rings back into a binary mask. A pixel is foreground when the
 * winding number of its center over all rings is non-zero: outer rings
 * (clockwise) contribute +1, holes (counter-clockwise) -1, so holes cancel
 * the fill. Used to verify that extracted contours reproduce the source.
 */
export function fillRings(rings: Ring[], width: number, height: number): Mask {
  const mask = createMask(width, height);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      if (windingNumber(rings, x + 0.5, y + 0.5) !== 0) {
        mask.data[y * width + x] = 1;
      }
    }
  }
  return mask;
}

function windingNumber(rings: Ring[], px: number, py: number): number {
  let wn = 0;
  for (const ring of rings) {
    const v = ring.vertices;
    for (let i = 0; i < v.length; i++) {
      const a = v[i];
      const b = v[(i + 1) % v.length];
      if (a.y <= py) {
        if (b.y > py && isLeft(a.x, a.y, b.x, b.y, px, py) > 0) wn++;
      } else {
        if (b.y <= py && isLeft(a.x, a.y, b.x, b.y, px, py) < 0) wn--;
      }
    }
  }
  return wn;
}

function isLeft(
  ax: number,
  ay: number,
  bx: number,
  by: number,
  px: number,
  py: number,
): number {
  return (bx - ax) * (py - ay) - (px - ax) * (by - ay);
}
