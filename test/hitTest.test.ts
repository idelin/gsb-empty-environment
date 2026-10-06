import { test } from 'node:test';
import assert from 'node:assert/strict';
import { caretX, hitTest, layoutLine } from '../src/index.ts';
import { mixedRuns, rtlOnlyRuns } from './fixtures.ts';

const line = layoutLine(mixedRuns());

test('ltr run: glyph midpoint splits leading/trailing offsets', () => {
  assert.deepEqual(hitTest(line, 3), { offset: 0, affinity: 'left' });
  assert.deepEqual(hitTest(line, 7), { offset: 1, affinity: 'right' });
  assert.deepEqual(hitTest(line, 15), { offset: 2, affinity: 'right' });
  assert.deepEqual(hitTest(line, 20), { offset: 2, affinity: 'left' });
});

test('ltr ligature: whole cluster maps to its start/end offsets', () => {
  assert.deepEqual(hitTest(line, 26), { offset: 2, affinity: 'left' });
  // Offset 4 also owns the caret at x=50 (start of the rtl run); the hit
  // at x=34 takes the leftmost of the two visual positions.
  assert.deepEqual(hitTest(line, 28), { offset: 4, affinity: 'left' });
});

test('rtl run: visual halves map to mirrored logical offsets', () => {
  // Glyph [6,7) occupies x 34..42; its trailing offset 7 is on the left.
  assert.deepEqual(hitTest(line, 35), { offset: 7, affinity: 'left' });
  assert.deepEqual(hitTest(line, 40), { offset: 6, affinity: 'right' });
  // Glyph [4,5) occupies x 42..50; its leading offset 4 is on the right.
  assert.deepEqual(hitTest(line, 43), { offset: 5, affinity: 'left' });
  assert.deepEqual(hitTest(line, 48), { offset: 4, affinity: 'right' });
});

test('run boundary adjudicated by click side, affinity marks visual side', () => {
  // Boundary at x=34: left side belongs to run 0 (offset 4), right side
  // to run 1 (offset 7). Both carets sit at the leftmost visual position
  // of their logical offset.
  assert.deepEqual(hitTest(line, 33.9), { offset: 4, affinity: 'left' });
  assert.deepEqual(hitTest(line, 34), { offset: 7, affinity: 'left' });
  // Boundary at x=50: mirror image; both carets take the rightmost
  // visual position of their offset.
  assert.deepEqual(hitTest(line, 49.9), { offset: 4, affinity: 'right' });
  assert.deepEqual(hitTest(line, 50), { offset: 7, affinity: 'right' });
});

test('zero-width glyph: neighbours collapse onto the same x', () => {
  // The zero-width mark [5,6) sits at x=42 and never captures a hit;
  // clicks land on the adjacent wide glyphs instead.
  assert.deepEqual(hitTest(line, 41.9), { offset: 6, affinity: 'right' });
  assert.deepEqual(hitTest(line, 42), { offset: 5, affinity: 'left' });
  // Offsets 5 and 6 are both reachable and both render at x=42.
  assert.equal(caretX(line, { offset: 5, affinity: 'left' }), 42);
  assert.equal(caretX(line, { offset: 6, affinity: 'right' }), 42);
});

test('clicks outside the line clamp to the nearest line edge', () => {
  assert.deepEqual(hitTest(line, -10), { offset: 0, affinity: 'left' });
  assert.deepEqual(hitTest(line, 1000), { offset: 8, affinity: 'right' });
});

test('empty line always returns the origin caret', () => {
  assert.deepEqual(hitTest(layoutLine([]), 42), {
    offset: 0,
    affinity: 'left',
  });
});

test('rtl-only line: x increases as logical offset decreases', () => {
  const rtl = layoutLine(rtlOnlyRuns());
  assert.deepEqual(hitTest(rtl, 1), { offset: 3, affinity: 'left' });
  assert.deepEqual(hitTest(rtl, 5), { offset: 2, affinity: 'right' });
  assert.deepEqual(hitTest(rtl, 8), { offset: 2, affinity: 'left' });
  assert.deepEqual(hitTest(rtl, 17), { offset: 0, affinity: 'right' });
});

test('caretX resolves ambiguous boundary offsets by affinity', () => {
  assert.equal(caretX(line, { offset: 4, affinity: 'left' }), 34);
  assert.equal(caretX(line, { offset: 4, affinity: 'right' }), 50);
  assert.equal(caretX(line, { offset: 7, affinity: 'left' }), 34);
  assert.equal(caretX(line, { offset: 7, affinity: 'right' }), 50);
  // Unambiguous offsets ignore affinity.
  assert.equal(caretX(line, { offset: 0, affinity: 'left' }), 0);
  assert.equal(caretX(line, { offset: 2, affinity: 'right' }), 20);
  assert.equal(caretX(line, { offset: 8, affinity: 'right' }), 62);
});

test('hitTest/caretX round-trip at run boundaries', () => {
  for (const x of [33.9, 34, 49.9, 50]) {
    const caret = hitTest(line, x);
    assert.equal(caretX(line, caret), x === 33.9 || x === 34 ? 34 : 50);
  }
});
