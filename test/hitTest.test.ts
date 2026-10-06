import test from 'node:test';
import assert from 'node:assert/strict';
import { hitTest, layoutLine } from '../src/index.ts';
import { mixedLine } from './fixtures.ts';

const layout = layoutLine(mixedLine());

test('clicks inside LTR glyphs split at the midpoint', () => {
  assert.deepEqual(hitTest(layout, 4), { offset: 0, affinity: 'left' });
  assert.deepEqual(hitTest(layout, 6), { offset: 1, affinity: 'right' });
  assert.deepEqual(hitTest(layout, 14), { offset: 1, affinity: 'left' });
  assert.deepEqual(hitTest(layout, 16), { offset: 2, affinity: 'right' });
});

test('clicks inside RTL glyphs map in reverse', () => {
  // Glyph [5,6) spans x [20,28): its left edge is offset 6.
  assert.deepEqual(hitTest(layout, 22), { offset: 6, affinity: 'left' });
  assert.deepEqual(hitTest(layout, 26), { offset: 5, affinity: 'right' });
  // Glyph [3,4) spans x [36,44): moving right decreases the offset.
  assert.deepEqual(hitTest(layout, 38), { offset: 4, affinity: 'left' });
  assert.deepEqual(hitTest(layout, 42), { offset: 3, affinity: 'right' });
});

test('direction boundary at x=20 is resolved by affinity', () => {
  // Same visual x, two logical positions: offset 2 (end of LTR run)
  // on the left side, offset 6 (end of RTL run) on the right side.
  assert.deepEqual(hitTest(layout, 19.9), { offset: 2, affinity: 'right' });
  assert.deepEqual(hitTest(layout, 20), { offset: 6, affinity: 'left' });
});

test('direction boundary at x=44 is resolved by affinity', () => {
  // Offset 3 (start of RTL run) on the left, offset 6 (start of the
  // trailing LTR run) on the right.
  assert.deepEqual(hitTest(layout, 43.9), { offset: 3, affinity: 'right' });
  assert.deepEqual(hitTest(layout, 44), { offset: 6, affinity: 'left' });
});

test('zero-width glyph is never hit directly', () => {
  // The zero-width glyph sits at x=20; clicks there and nearby resolve
  // to neighbouring glyph edges, never to offsets inside [2,3) edges
  // other than through the neighbour's own edge.
  assert.deepEqual(hitTest(layout, 19), { offset: 2, affinity: 'right' });
  assert.deepEqual(hitTest(layout, 20), { offset: 6, affinity: 'left' });
  assert.deepEqual(hitTest(layout, 21), { offset: 6, affinity: 'left' });
});

test('clicks outside the line clamp to the nearest edge', () => {
  assert.deepEqual(hitTest(layout, -100), { offset: 0, affinity: 'left' });
  assert.deepEqual(hitTest(layout, 0), { offset: 0, affinity: 'left' });
  assert.deepEqual(hitTest(layout, 64), { offset: 8, affinity: 'right' });
  assert.deepEqual(hitTest(layout, 1000), { offset: 8, affinity: 'right' });
});

test('empty line always returns offset 0', () => {
  const empty = layoutLine({ runs: [] });
  assert.deepEqual(hitTest(empty, 0), { offset: 0, affinity: 'left' });
  assert.deepEqual(hitTest(empty, 50), { offset: 0, affinity: 'left' });
});

test('line of only zero-width glyphs clamps to the line edges', () => {
  const zw = layoutLine({
    runs: [
      {
        direction: 'ltr',
        glyphs: [
          { start: 0, end: 1, width: 0 },
          { start: 1, end: 2, width: 0 },
        ],
      },
    ],
  });
  assert.deepEqual(hitTest(zw, 0), { offset: 0, affinity: 'left' });
  assert.deepEqual(hitTest(zw, 10), { offset: 2, affinity: 'right' });
});
