import type { TextLine } from '../src/index.ts';

/**
 * Mixed-direction line with a zero-width glyph.
 *
 * Logical text (8 chars): a b <zw> א ב ג x y
 *   [0,1) 'a'  w10   [1,2) 'b'  w10   [2,3) combining mark, w0
 *   [3,4) 'א'  w8    [4,5) 'ב'  w8    [5,6) 'ג'  w8   (RTL run)
 *   [6,7) 'x'  w10   [7,8) 'y'  w10
 *
 * Visual layout (line width 64):
 *   x: 0    10   20   20  28   36   44   54
 *      a    b    <zw> ג   ב    א    x    y
 */
export function mixedLine(): TextLine {
  return {
    runs: [
      {
        direction: 'ltr',
        glyphs: [
          { start: 0, end: 1, width: 10 },
          { start: 1, end: 2, width: 10 },
          { start: 2, end: 3, width: 0 },
        ],
      },
      {
        direction: 'rtl',
        glyphs: [
          { start: 3, end: 4, width: 8 },
          { start: 4, end: 5, width: 8 },
          { start: 5, end: 6, width: 8 },
        ],
      },
      {
        direction: 'ltr',
        glyphs: [
          { start: 6, end: 7, width: 10 },
          { start: 7, end: 8, width: 10 },
        ],
      },
    ],
  };
}
