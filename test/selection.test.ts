import test from 'node:test';
import assert from 'node:assert/strict';
import { layoutLine, selectionRects } from '../src/index.ts';
import { mixedLine } from './fixtures.ts';

const layout = layoutLine(mixedLine());

test('full selection merges into one rectangle', () => {
  assert.deepEqual(selectionRects(layout, 0, 8, 20), [
    { x: 0, y: 0, width: 64, height: 20 },
  ]);
});

test('selection spanning a direction boundary can be disjoint', () => {
  // Offsets [1,5): 'b' + zero-width mark at x [10,20), then Hebrew
  // glyphs [3,4) and [4,5) at x [28,44). Gap at [20,28) = unselected
  // Hebrew glyph [5,6).
  assert.deepEqual(selectionRects(layout, 1, 5), [
    { x: 10, y: 0, width: 10, height: 1 },
    { x: 28, y: 0, width: 16, height: 1 },
  ]);
});

test('disjoint selection across both boundaries', () => {
  // Offsets [5,7): Hebrew glyph [5,6) at x [20,28) and 'x' at [44,54).
  assert.deepEqual(selectionRects(layout, 5, 7), [
    { x: 20, y: 0, width: 8, height: 1 },
    { x: 44, y: 0, width: 10, height: 1 },
  ]);
});

test('selection inside the RTL run is visually reversed but contiguous', () => {
  // Offsets [4,6) select the two rightmost visual glyphs of the run.
  assert.deepEqual(selectionRects(layout, 4, 6), [
    { x: 20, y: 0, width: 16, height: 1 },
  ]);
  assert.deepEqual(selectionRects(layout, 3, 6), [
    { x: 20, y: 0, width: 24, height: 1 },
  ]);
});

test('contiguous cross-run selection merges touching rectangles', () => {
  // Offsets [1,7): everything except 'a' and 'y' — one visual run.
  assert.deepEqual(selectionRects(layout, 1, 7), [
    { x: 10, y: 0, width: 44, height: 1 },
  ]);
});

test('anchor/focus order does not matter', () => {
  assert.deepEqual(selectionRects(layout, 7, 1), selectionRects(layout, 1, 7));
});

test('zero-width-only selection yields a zero-width rectangle', () => {
  assert.deepEqual(selectionRects(layout, 2, 3), [
    { x: 20, y: 0, width: 0, height: 1 },
  ]);
});

test('empty selection yields no rectangles', () => {
  assert.deepEqual(selectionRects(layout, 4, 4), []);
  assert.deepEqual(selectionRects(layout, 0, 0), []);
});

test('y and height are passed through', () => {
  assert.deepEqual(selectionRects(layout, 0, 1, 18, 5), [
    { x: 0, y: 5, width: 10, height: 18 },
  ]);
});
