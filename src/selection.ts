import type { LineLayout } from './layout.ts';
import type { Rect } from './types.ts';

/**
 * Map a logical selection range [anchor, focus) to visual rectangles.
 *
 * Because a contiguous logical range can be visually non-contiguous in
 * mixed-direction text, the result is a list of rectangles in visual
 * (left-to-right) order. Rectangles that touch exactly are merged, so
 * genuinely disjoint regions stay as separate entries. A glyph is
 * selected when its logical range intersects the selection; zero-width
 * glyphs contribute zero-width rectangles.
 */
export function selectionRects(
  layout: LineLayout,
  anchor: number,
  focus: number,
  height = 1,
  y = 0,
): Rect[] {
  const lo = Math.min(anchor, focus);
  const hi = Math.max(anchor, focus);
  const rects: Rect[] = [];

  for (const glyph of layout.glyphs) {
    if (glyph.end <= lo || glyph.start >= hi) continue;
    const last = rects[rects.length - 1];
    if (last && last.x + last.width === glyph.x) {
      last.width += glyph.width;
    } else {
      rects.push({ x: glyph.x, y, width: glyph.width, height });
    }
  }
  return rects;
}
