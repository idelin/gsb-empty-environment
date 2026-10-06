import { test } from 'node:test';
import assert from 'node:assert/strict';
import { layoutLine, offsetXInRun, selectionRects } from '../src/index.ts';
import type { CaretPosition } from '../src/index.ts';
import { mixedRuns, rtlOnlyRuns } from './fixtures.ts';

const line = layoutLine(mixedRuns());
const caret = (offset: number): CaretPosition => ({ offset, affinity: 'left' });

test('selection inside one ltr run is a single rect', () => {
  assert.deepEqual(selectionRects(line, caret(0), caret(2)), [
    { x: 0, width: 20 },
  ]);
});

test('partial ligature selection interpolates across the cluster', () => {
  assert.deepEqual(selectionRects(line, caret(2), caret(3)), [
    { x: 20, width: 7 },
  ]);
});

test('selection inside an rtl run is mirrored but normalized', () => {
  assert.deepEqual(selectionRects(line, caret(6), caret(7)), [
    { x: 34, width: 8 },
  ]);
  assert.deepEqual(selectionRects(line, caret(4), caret(7)), [
    { x: 34, width: 16 },
  ]);
});

test('logically contiguous selection across runs is visually disjoint', () => {
  // Offsets 1..6 cover run 0 ([1,4) -> x 10..34) and run 1 ([4,6) ->
  // x 42..50), leaving the unselected glyph [6,7) as a visual gap.
  assert.deepEqual(selectionRects(line, caret(1), caret(6)), [
    { x: 10, width: 24 },
    { x: 42, width: 8 },
  ]);
});

test('drag direction does not matter', () => {
  assert.deepEqual(selectionRects(line, caret(6), caret(1)), [
    { x: 10, width: 24 },
    { x: 42, width: 8 },
  ]);
});

test('full-line selection returns one rect per run in visual order', () => {
  assert.deepEqual(selectionRects(line, caret(0), caret(8)), [
    { x: 0, width: 34 },
    { x: 34, width: 16 },
    { x: 50, width: 12 },
  ]);
});

test('zero-width glyph contributes no selection width', () => {
  // [5,6) is the zero-width mark: selecting exactly it yields nothing.
  assert.deepEqual(selectionRects(line, caret(5), caret(6)), []);
  // [4,6) covers a wide glyph plus the mark: width comes from the glyph.
  assert.deepEqual(selectionRects(line, caret(4), caret(6)), [
    { x: 42, width: 8 },
  ]);
});

test('collapsed selection is empty', () => {
  assert.deepEqual(selectionRects(line, caret(3), caret(3)), []);
});

test('rtl-only line: selection rect covers the mirrored span', () => {
  const rtl = layoutLine(rtlOnlyRuns());
  assert.deepEqual(selectionRects(rtl, caret(0), caret(2)), [
    { x: 6, width: 12 },
  ]);
});

test('offsetXInRun clamps to the run bounds', () => {
  const run = line.runs[1]!;
  assert.equal(offsetXInRun(run, -5), 50);
  assert.equal(offsetXInRun(run, 99), 34);
});
