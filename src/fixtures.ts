import { box, textBox, type GlyphFixture, type UINode } from './layout.ts';

/** Hand-written glyph metrics (unitsPerEm = 1000). Stands in for a font file;
 *  keeps text measurement deterministic and browser-free. */
export const GLYPHS: GlyphFixture = {
  unitsPerEm: 1000,
  fallback: 500,
  advances: {
    ' ': 250,
    P: 650, i: 250, n: 550, c: 500, h: 550, d: 550, e: 520, m: 850, o: 540,
    T: 600, a: 520, p: 550, l: 250, y: 500, u: 550, t: 350, r: 350, g: 540,
    s: 460, k: 520, D: 700, x: 500, '1': 500, '2': 500,
  },
};

/** A small UI tree used by the demo and the hit-test tests. */
export function buildFixtureTree(): UINode {
  return box('screen', 0, 0, 390, 844, [
    box('card', 16, 16, 358, 120, [
      textBox('card-title', 32, 32, 'Pinch demo', 16, 20, GLYPHS),
    ]),
    box('button', 24, 160, 160, 48, [
      textBox('button-label', 40, 172, 'Tap me', 14, 20, GLYPHS),
    ]),
    box('dialog', 60, 300, 270, 200, [
      textBox('dialog-title', 76, 316, 'Drag 12px', 16, 20, GLYPHS),
    ]),
  ]);
}
