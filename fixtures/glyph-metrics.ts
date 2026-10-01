/**
 * Fixture glyph metrics: advance widths per 1000 units of em, keyed by char.
 * Core layout math uses these tables only — no browser text measurement.
 */
export const GLYPH_ADVANCES: Record<string, number> = {
  ' ': 278,
  A: 681, B: 667, C: 722, D: 722, E: 611, F: 556, G: 759, H: 722,
  I: 239, J: 239, K: 681, L: 577, M: 833, N: 722, O: 759, P: 611,
  Q: 759, R: 667, S: 611, T: 611, U: 722, V: 681, W: 908, X: 681,
  Y: 681, Z: 611,
  a: 556, b: 589, c: 509, d: 589, e: 561, f: 292, g: 589, h: 589,
  i: 239, j: 239, k: 531, l: 239, m: 863, n: 589, o: 579, p: 589,
  q: 589, r: 337, s: 431, t: 331, u: 589, v: 531, w: 763, x: 531,
  y: 531, z: 449,
};

export const FALLBACK_ADVANCE = 500;

export function measureText(text: string, fontSize: number): number {
  let units = 0;
  for (const ch of text) {
    units += GLYPH_ADVANCES[ch] ?? FALLBACK_ADVANCE;
  }
  return Math.round((units * fontSize) / 10) / 100;
}
