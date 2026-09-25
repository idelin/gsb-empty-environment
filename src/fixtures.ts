import type { GlyphMetrics } from './measure.ts';
import type { HeaderDoc } from './types.ts';

/**
 * Fixture-given glyph metrics: a deterministic stand-in for a browser text
 * measurement engine. Every width in the system derives from this table.
 */
export const glyphMetrics: GlyphMetrics = {
  charWidth: {
    A: 8, B: 8, C: 8, D: 9, E: 7, F: 7, G: 9, H: 9, I: 3, J: 5, K: 8, L: 7,
    M: 11, N: 9, O: 9, P: 8, Q: 9, R: 8, S: 7, T: 7, U: 9, V: 8, W: 11, X: 8,
    Y: 7, Z: 8,
    a: 7, b: 7, c: 6, d: 7, e: 7, f: 4, g: 7, h: 7, i: 3, j: 3, k: 6, l: 3,
    m: 11, n: 7, o: 7, p: 7, q: 7, r: 4, s: 6, t: 4, u: 7, v: 6, w: 9, x: 6,
    y: 6, z: 6,
    ' ': 3, '-': 4, '_': 6, '.': 3, '0': 7, '1': 7, '2': 7, '3': 7, '4': 7,
    '5': 7, '6': 7, '7': 7, '8': 7, '9': 7,
  },
  defaultWidth: 8,
  padding: 12,
  minColumnWidth: 48,
};

/**
 * Fixture node tree. Note `c-math` and `c-eng` share the display name
 * "Score": identity is by id, never by name.
 */
export function sampleDoc(): HeaderDoc {
  return {
    hidden: new Set<string>(),
    collapsed: new Set<string>(),
    root: {
      kind: 'group',
      id: 'root',
      name: 'Root',
      children: [
        {
          kind: 'group',
          id: 'g-basic',
          name: 'Basic',
          children: [
            { kind: 'leaf', id: 'c-name', name: 'Name' },
            { kind: 'leaf', id: 'c-age', name: 'Age' },
          ],
        },
        {
          kind: 'group',
          id: 'g-metrics',
          name: 'Metrics',
          children: [
            {
              kind: 'group',
              id: 'g-scores',
              name: 'Scores',
              children: [
                { kind: 'leaf', id: 'c-math', name: 'Score' },
                { kind: 'leaf', id: 'c-eng', name: 'Score' },
              ],
            },
            { kind: 'leaf', id: 'c-rank', name: 'Rank' },
          ],
        },
        { kind: 'leaf', id: 'c-note', name: 'Note' },
      ],
    },
  };
}
