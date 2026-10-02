import type { Placement } from './types.ts';

/**
 * UV mapping for a placement. UV (0,0) is the top-left texel of the
 * original (unrotated) texture; (1,1) the bottom-right. The mapping returns
 * page coordinates in the same normalized style is NOT used — we return
 * absolute page pixels so callers can sample the atlas directly.
 *
 * Rotation is 90 degrees clockwise: the original top-left texel lands on the
 * top-right corner of the placed rectangle.
 */
export function uvToPage(
  p: Placement,
  u: number,
  v: number,
): { x: number; y: number } {
  if (u < 0 || u > 1 || v < 0 || v > 1) {
    throw new Error(`uv out of range: (${u}, ${v})`);
  }
  if (!p.rotated) {
    return { x: p.x + u * p.width, y: p.y + v * p.height };
  }
  // Placed box is (origH x origW). 90 CW: page_x = x + (1 - v) * origH,
  // page_y = y + u * origW. Placed width = origH, placed height = origW.
  return { x: p.x + (1 - v) * p.width, y: p.y + u * p.height };
}

/** Inverse of uvToPage. */
export function pageToUv(
  p: Placement,
  x: number,
  y: number,
): { u: number; v: number } {
  if (!p.rotated) {
    return { u: (x - p.x) / p.width, v: (y - p.y) / p.height };
  }
  return { u: (y - p.y) / p.height, v: 1 - (x - p.x) / p.width };
}

/** The four UV corners mapped to page coordinates, in TL, TR, BR, BL order. */
export function uvCorners(p: Placement): { x: number; y: number }[] {
  return [
    uvToPage(p, 0, 0),
    uvToPage(p, 1, 0),
    uvToPage(p, 1, 1),
    uvToPage(p, 0, 1),
  ];
}
