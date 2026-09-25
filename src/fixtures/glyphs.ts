/**
 * Glyph-metrics fixture.
 *
 * In a browser you would measure text with canvas/DOM APIs. Here the
 * metrics are given as data, so all layout math is pure computation
 * and fully testable under Node.js.
 */
export interface GlyphMetrics {
  /** Advance width per character, in px. Missing chars use `defaultAdvance`. */
  advances: Record<string, number>;
  defaultAdvance: number;
  /** Width used for any CJK / wide code point. */
  wideAdvance: number;
  lineHeight: number;
  /** Horizontal padding added on both sides of a cell. */
  cellPaddingX: number;
  cellPaddingY: number;
}

export const fixtureGlyphs: GlyphMetrics = {
  advances: {
    " ": 4,
    ".": 4,
    ",": 4,
    ":": 4,
    "-": 5,
    "/": 5,
    "0": 8,
    "1": 8,
    "2": 8,
    "3": 8,
    "4": 8,
    "5": 8,
    "6": 8,
    "7": 8,
    "8": 8,
    "9": 8,
    "%": 12,
    "A": 9,
    "B": 9,
    "C": 9,
    "D": 10,
    "E": 8,
    "F": 8,
    "G": 10,
    "H": 10,
    "I": 4,
    "J": 7,
    "K": 9,
    "L": 8,
    "M": 11,
    "N": 10,
    "O": 10,
    "P": 9,
    "Q": 10,
    "R": 9,
    "S": 9,
    "T": 9,
    "U": 10,
    "V": 9,
    "W": 13,
    "X": 9,
    "Y": 9,
    "Z": 9,
    "a": 8,
    "b": 8,
    "c": 7,
    "d": 8,
    "e": 8,
    "f": 5,
    "g": 8,
    "h": 8,
    "i": 4,
    "j": 4,
    "k": 7,
    "l": 4,
    "m": 12,
    "n": 8,
    "o": 8,
    "p": 8,
    "q": 8,
    "r": 5,
    "s": 7,
    "t": 5,
    "u": 8,
    "v": 7,
    "w": 11,
    "x": 7,
    "y": 7,
    "z": 7,
  },
  defaultAdvance: 8,
  wideAdvance: 16,
  lineHeight: 20,
  cellPaddingX: 6,
  cellPaddingY: 4,
};

function isWide(codePoint: number): boolean {
  return (
    codePoint >= 0x1100 &&
    (codePoint <= 0x115f || // Hangul Jamo
      codePoint === 0x2329 ||
      codePoint === 0x232a ||
      (codePoint >= 0x2e80 && codePoint <= 0xa4cf) || // CJK radicals .. Yi
      (codePoint >= 0xac00 && codePoint <= 0xd7a3) || // Hangul syllables
      (codePoint >= 0xf900 && codePoint <= 0xfaff) || // CJK compat ideographs
      (codePoint >= 0xfe30 && codePoint <= 0xfe6f) || // CJK compat forms
      (codePoint >= 0xff00 && codePoint <= 0xff60) || // Fullwidth forms
      (codePoint >= 0x20000 && codePoint <= 0x3fffd))
  );
}

/** Pure text measurement driven only by the fixture metrics. */
export function measureText(text: string, metrics: GlyphMetrics = fixtureGlyphs): number {
  let width = 0;
  for (const ch of text) {
    const codePoint = ch.codePointAt(0) ?? 0;
    if (isWide(codePoint)) {
      width += metrics.wideAdvance;
    } else {
      width += metrics.advances[ch] ?? metrics.defaultAdvance;
    }
  }
  return width;
}
