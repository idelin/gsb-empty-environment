/**
 * Deterministic glyph metrics supplied entirely by fixtures.
 * No browser / platform text shaping is involved: every glyph has an
 * explicit advance width and measuring a string is a pure table lookup.
 */

export interface GlyphMetricsTable {
  /** Advance width per character. Missing characters use `defaultAdvance`. */
  readonly advances: Readonly<Record<string, number>>;
  readonly defaultAdvance: number;
}

/** Fixture table roughly modelling a proportional sans font at 16px. */
export const FIXTURE_GLYPH_METRICS: GlyphMetricsTable = {
  defaultAdvance: 8,
  advances: {
    " ": 4,
    i: 3,
    l: 3,
    t: 5,
    r: 5,
    f: 5,
    ".": 4,
    ",": 4,
    ":": 4,
    "%": 13,
    "0": 9,
    "1": 9,
    "2": 9,
    "3": 9,
    "4": 9,
    "5": 9,
    "6": 9,
    "7": 9,
    "8": 9,
    "9": 9,
    W: 15,
    M: 13,
    Q: 12,
  },
};

/** Sum of per-glyph advance widths. Pure function of the fixture table. */
export function measureText(text: string, metrics: GlyphMetricsTable): number {
  let width = 0;
  for (const char of text) {
    width += metrics.advances[char] ?? metrics.defaultAdvance;
  }
  return width;
}
