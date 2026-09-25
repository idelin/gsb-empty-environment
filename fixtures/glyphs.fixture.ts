import type { GlyphMetrics } from '../src/types.ts';

/**
 * Fixture glyph advances (px). Deterministic, hand-authored: the core never
 * consults a browser layout engine for measurement.
 */
export const fixtureGlyphs: GlyphMetrics = {
  defaultAdvance: 6,
  advance: {
    ' ': 3,
    '-': 4,
    a: 6, b: 6, c: 5, d: 6, e: 6, g: 6, h: 6, i: 2, l: 2, m: 9,
    n: 6, o: 6, p: 6, r: 4, s: 5, t: 3, u: 6, w: 8, x: 5, y: 5,
    '1': 6, '2': 6, '3': 6,
  },
};
