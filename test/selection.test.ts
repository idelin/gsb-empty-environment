import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildGrid } from '../src/grid.ts';
import { loadGlyphFixture, loadTableFixture } from '../src/fixtures.ts';
import { expandToFixedPoint, normalizeRect, selectRange } from '../src/selection.ts';
import type { Rect } from '../src/types.ts';

const grid = buildGrid(loadTableFixture(), loadGlyphFixture());

test('touching a merged region selects the whole region', () => {
  // Single slot (0,1) is covered by Alpha (0,0)-(0,1).
  const rect = selectRange(grid, { anchor: { row: 0, col: 1 }, focus: { row: 0, col: 1 } });
  assert.deepEqual(rect, { top: 0, left: 0, bottom: 0, right: 1 });
});

test('partial overlap with a 2x2 merge pulls in the whole block', () => {
  // (2,1) is inside Eta (rows 2-3, cols 0-1); covering Eta then clips
  // Kappa at (3,2), so the chain continues to cols 0-3.
  const rect = selectRange(grid, { anchor: { row: 2, col: 1 }, focus: { row: 2, col: 2 } });
  assert.deepEqual(rect, { top: 2, left: 0, bottom: 3, right: 3 });
});

test('chained merges expand to a fixed point and terminate', () => {
  // (1,1)-(2,2) clips Eta -> grows into Kappa -> grows into Gamma -> whole grid.
  const rect = selectRange(grid, { anchor: { row: 1, col: 1 }, focus: { row: 2, col: 2 } });
  assert.deepEqual(rect, { top: 0, left: 0, bottom: 3, right: 3 });
});

test('selections clear of merges stay untouched', () => {
  const rect = selectRange(grid, { anchor: { row: 2, col: 2 }, focus: { row: 2, col: 3 } });
  assert.deepEqual(rect, { top: 2, left: 2, bottom: 2, right: 3 });
});

test('expansion is idempotent (it is a fixed point)', () => {
  const once = selectRange(grid, { anchor: { row: 1, col: 1 }, focus: { row: 2, col: 2 } });
  const twice = expandToFixedPoint(grid, once);
  assert.deepEqual(twice, once);
});

test('reversed drag normalizes before expanding', () => {
  const rect = selectRange(grid, { anchor: { row: 2, col: 2 }, focus: { row: 1, col: 1 } });
  assert.deepEqual(rect, { top: 0, left: 0, bottom: 3, right: 3 });
});

test('out-of-range endpoints are clamped to grid bounds', () => {
  const rect = selectRange(grid, { anchor: { row: -5, col: -5 }, focus: { row: 99, col: 99 } });
  assert.deepEqual(rect, { top: 0, left: 0, bottom: 3, right: 3 });
});

test('normalizeRect orders corners', () => {
  const rect: Rect = normalizeRect({ row: 3, col: 1 }, { row: 1, col: 2 });
  assert.deepEqual(rect, { top: 1, left: 1, bottom: 3, right: 2 });
});
