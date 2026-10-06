import type { GlyphRun } from '../src/index.ts';

/**
 * Mixed-direction line used across the test suite:
 *
 *   run 0 (ltr): glyphs [0,1)w10 [1,2)w10 [2,4)w14   -> x 0..34
 *   run 1 (rtl): glyphs [4,5)w8  [5,6)w0  [6,7)w8    -> x 34..50
 *   run 2 (ltr): glyph  [7,8)w12                     -> x 50..62
 *
 * Run 1 is stored in logical order, so its first glyph ([4,5)) sits at
 * the run's right edge and the zero-width mark ([5,6)) shares x=42 with
 * the boundary between its neighbours.
 *
 *   visual: | A 0..10 | B 10..20 | CD 20..34 | ב 34..42 | ◌+א 42..50 | E 50..62 |
 *   logical:  0    1    2        4            6     5    4             7        8
 */
export function mixedRuns(): GlyphRun[] {
  return [
    {
      direction: 'ltr',
      glyphs: [
        { start: 0, end: 1, width: 10 },
        { start: 1, end: 2, width: 10 },
        { start: 2, end: 4, width: 14 },
      ],
    },
    {
      direction: 'rtl',
      glyphs: [
        { start: 4, end: 5, width: 8 },
        { start: 5, end: 6, width: 0 },
        { start: 6, end: 7, width: 8 },
      ],
    },
    {
      direction: 'ltr',
      glyphs: [{ start: 7, end: 8, width: 12 }],
    },
  ];
}

/** Single RTL run, logical offsets 0..3. */
export function rtlOnlyRuns(): GlyphRun[] {
  return [
    {
      direction: 'rtl',
      glyphs: [
        { start: 0, end: 1, width: 6 },
        { start: 1, end: 2, width: 6 },
        { start: 2, end: 3, width: 6 },
      ],
    },
  ];
}
