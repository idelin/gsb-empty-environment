/**
 * Fixture glyph metrics. Deterministic, integer-unit widths.
 * This is the ONLY source of glyph measurement; nothing is delegated
 * to a browser layout engine.
 */
export const GLYPH_WIDTHS: Readonly<Record<string, number>> = {
  ' ': 0, // spaces become glue nodes, never measured as glyphs
  '-': 3,
  a: 5, b: 5, c: 4, d: 5, e: 5, f: 3, g: 5, h: 5, i: 2, j: 2, k: 4, l: 2,
  m: 8, n: 5, o: 5, p: 5, q: 5, r: 3, s: 4, t: 3, u: 5, v: 5, w: 7, x: 5,
  y: 5, z: 4,
  A: 7, B: 6, C: 7, D: 7, E: 6, F: 5, G: 7, H: 7, I: 3, J: 4, K: 6, L: 5,
  M: 9, N: 7, O: 7, P: 6, Q: 7, R: 6, S: 6, T: 6, U: 7, V: 7, W: 9, X: 7,
  Y: 6, Z: 6,
  '.': 2, ',': 2, ';': 2, ':': 2, '!': 2, '?': 3, "'": 2, '"': 3,
  '0': 5, '1': 5, '2': 5, '3': 5, '4': 5, '5': 5, '6': 5, '7': 5, '8': 5,
  '9': 5,
};

export const DEFAULT_GLYPH_WIDTH = 5;

export function glyphWidth(ch: string): number {
  const w = GLYPH_WIDTHS[ch];
  return w === undefined ? DEFAULT_GLYPH_WIDTH : w;
}

/** Width of a word/string in fixture units. */
export function measureText(text: string): number {
  let total = 0;
  for (const ch of text) total += glyphWidth(ch);
  return total;
}
