import type { LineLayout } from './layout.ts';
import type { CaretPosition } from './types.ts';

/**
 * Map a visual x coordinate to a logical insertion point.
 *
 * The glyph under `x` is split at its midpoint: the nearer visual edge
 * wins, and the edge's recorded offset/affinity becomes the result.
 * In RTL runs the edges carry swapped offsets, so the mapping is
 * automatically reversed. Zero-width glyphs are never hit directly;
 * clicks on them resolve to a neighbouring glyph edge. Clicks outside
 * the line clamp to the closest line edge.
 */
export function hitTest(layout: LineLayout, x: number): CaretPosition {
  const { glyphs } = layout;
  if (glyphs.length === 0) return { offset: 0, affinity: 'left' };

  const first = glyphs[0];
  const last = glyphs[glyphs.length - 1];
  if (x <= first.x) return { offset: first.leftOffset, affinity: 'left' };
  if (x >= last.x + last.width) {
    return { offset: last.rightOffset, affinity: 'right' };
  }

  for (const glyph of glyphs) {
    if (glyph.width === 0) continue;
    if (x < glyph.x + glyph.width) {
      const mid = glyph.x + glyph.width / 2;
      return x < mid
        ? { offset: glyph.leftOffset, affinity: 'left' }
        : { offset: glyph.rightOffset, affinity: 'right' };
    }
  }
  return { offset: last.rightOffset, affinity: 'right' };
}
