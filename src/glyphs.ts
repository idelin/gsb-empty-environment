// Glyph metrics supplied by the fixture (stands in for a font file).
// All measuring is done here from raw advance widths; no browser involved.

export interface FontMetrics {
  unitsPerEm: number;
  // Advance width of each glyph in font units.
  advances: Readonly<Record<string, number>>;
  defaultAdvance: number;
}

// A deterministic fixture font: 1000 upm, simple per-character advances.
export const fixtureFont: FontMetrics = {
  unitsPerEm: 1000,
  defaultAdvance: 500,
  advances: {
    " ": 250,
    i: 200,
    l: 200,
    m: 800,
    w: 720,
    a: 500,
    b: 520,
    c: 480,
    d: 520,
    e: 500,
    f: 300,
    g: 520,
    o: 540,
    r: 330,
    s: 460,
    t: 320,
    x: 500,
    A: 660,
    B: 620,
    C: 680,
    G: 720,
    R: 640,
    T: 600,
    "0": 560,
    "1": 560,
    "2": 560,
    "3": 560,
    "-": 330,
    ".": 240,
  },
};

export function glyphAdvance(ch: string, font: FontMetrics): number {
  const advance = font.advances[ch];
  return advance === undefined ? font.defaultAdvance : advance;
}

// Width of a string in CSS pixels at the given font size.
export function measureText(text: string, fontSize: number, font: FontMetrics): number {
  let units = 0;
  for (const ch of text) {
    units += glyphAdvance(ch, font);
  }
  return (units / font.unitsPerEm) * fontSize;
}

// Minimum content width: the widest unbreakable word.
export function minContentWidth(text: string, fontSize: number, font: FontMetrics): number {
  let widest = 0;
  for (const word of text.split(/\s+/)) {
    if (word.length === 0) continue;
    const width = measureText(word, fontSize, font);
    if (width > widest) widest = width;
  }
  return widest;
}
