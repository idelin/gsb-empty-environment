import type { GlyphMetrics } from '../src/layout.ts';

/**
 * Glyph metrics fixture: a small deterministic proportional font.
 * All layout tests and the demo measure text exclusively through this table.
 */
export const glyphFixture: GlyphMetrics = {
  defaultAdvance: 8,
  lineHeight: 16,
  advances: {
    ' ': 4,
    i: 3,
    l: 3,
    t: 5,
    m: 12,
    w: 11,
    W: 14,
    M: 13,
    '.': 3,
    ',': 3,
    '!': 4,
    '0': 8,
    '1': 8,
    '2': 8,
    '3': 8,
    '4': 8,
    '5': 8,
    '6': 8,
    '7': 8,
    '8': 8,
    '9': 8,
  },
};
