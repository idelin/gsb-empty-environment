import test from 'node:test';
import assert from 'node:assert/strict';
import { layoutLine } from '../src/index.ts';
import type { GlyphPosition } from '../src/index.ts';
import { mixedLine } from './fixtures.ts';

function table(layout: ReturnType<typeof layoutLine>): GlyphPosition[] {
  return layout.glyphs;
}

test('per-glyph position table for a mixed-direction line', () => {
  const layout = layoutLine(mixedLine());
  assert.equal(layout.width, 64);
  assert.equal(layout.length, 8);

  assert.deepEqual(table(layout), [
    // LTR run: glyphs advance left to right in logical order.
    { runIndex: 0, glyphIndex: 0, direction: 'ltr', start: 0, end: 1, x: 0, width: 10, leftOffset: 0, rightOffset: 1 },
    { runIndex: 0, glyphIndex: 1, direction: 'ltr', start: 1, end: 2, x: 10, width: 10, leftOffset: 1, rightOffset: 2 },
    // Zero-width glyph occupies no visual space at x = 20.
    { runIndex: 0, glyphIndex: 2, direction: 'ltr', start: 2, end: 3, x: 20, width: 0, leftOffset: 2, rightOffset: 3 },
    // RTL run [20, 44): first logical glyph is rightmost, edges swapped.
    { runIndex: 1, glyphIndex: 2, direction: 'rtl', start: 5, end: 6, x: 20, width: 8, leftOffset: 6, rightOffset: 5 },
    { runIndex: 1, glyphIndex: 1, direction: 'rtl', start: 4, end: 5, x: 28, width: 8, leftOffset: 5, rightOffset: 4 },
    { runIndex: 1, glyphIndex: 0, direction: 'rtl', start: 3, end: 4, x: 36, width: 8, leftOffset: 4, rightOffset: 3 },
    // Trailing LTR run.
    { runIndex: 2, glyphIndex: 0, direction: 'ltr', start: 6, end: 7, x: 44, width: 10, leftOffset: 6, rightOffset: 7 },
    { runIndex: 2, glyphIndex: 1, direction: 'ltr', start: 7, end: 8, x: 54, width: 10, leftOffset: 7, rightOffset: 8 },
  ]);
});

test('position table is sorted by visual x', () => {
  const layout = layoutLine(mixedLine());
  const xs = layout.glyphs.map((g) => g.x);
  assert.deepEqual(xs, [0, 10, 20, 20, 28, 36, 44, 54]);
});

test('empty line', () => {
  const layout = layoutLine({ runs: [] });
  assert.deepEqual(layout.glyphs, []);
  assert.equal(layout.width, 0);
  assert.equal(layout.length, 0);
});

test('rtl-only line reverses glyph order', () => {
  const layout = layoutLine({
    runs: [
      {
        direction: 'rtl',
        glyphs: [
          { start: 0, end: 2, width: 6 },
          { start: 2, end: 4, width: 6 },
        ],
      },
    ],
  });
  assert.deepEqual(
    layout.glyphs.map((g) => [g.start, g.x]),
    [[2, 0], [0, 6]],
  );
});
