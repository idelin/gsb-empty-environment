import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { hitTest, selectRange } from '../src/mapping.ts';
import { metricsV1 } from '../fixtures/metrics.ts';
import { hebrewBlock, hebrewMixed } from '../fixtures/trees.ts';
import { lineText } from './helpers.ts';

test('rtl paragraph renders clusters in visual order', () => {
  const engine = new TruncationEngine(hebrewBlock, metricsV1, { width: 200, maxLines: 1, mode: 'end' });
  const r = engine.lastLayout;
  assert.equal(r.paraDir, 'rtl');
  assert.equal(lineText(r, 0), 'הדגבא'); // reversed logical order
  // x grows left to right along the visual order
  const xs = r.lines[0].map((it) => it.x);
  assert.deepEqual(xs, [...xs].sort((a, b) => a - b));
});

test('mixed-direction runs reorder per embedding levels', () => {
  const engine = new TruncationEngine(hebrewMixed, metricsV1, { width: 300, maxLines: 1, mode: 'end' });
  const r = engine.lastLayout;
  assert.equal(r.paraDir, 'rtl');
  // rtl runs read right-to-left, the latin run keeps its internal order
  assert.equal(lineText(r, 0), 'והד abc גבא');
});

test('hit test maps visual positions back to source offsets in rtl text', () => {
  const engine = new TruncationEngine(hebrewBlock, metricsV1, { width: 200, maxLines: 1, mode: 'end' });
  const r = engine.lastLayout;
  // visual order: ה(0..6) ד(6..12) ג(12..18) ב(18..24) א(24..30)
  // source offsets: א=0 ב=1 ג=2 ד=3 ה=4
  assert.equal(hitTest(r, 0, 1), 5);   // left half of ה -> its source end
  assert.equal(hitTest(r, 0, 5), 4);   // right half of ה -> its source start
  assert.equal(hitTest(r, 0, 25), 1);  // left half of א -> its source end
  assert.equal(hitTest(r, 0, 29), 0);  // right half of א -> its source start
});

test('selection direction is preserved for rtl text', () => {
  const engine = new TruncationEngine(hebrewBlock, metricsV1, { width: 200, maxLines: 1, mode: 'end' });
  const r = engine.lastLayout;

  const forward = selectRange(r, 1, 3); // בג in source order
  assert.equal(forward.backward, false);
  assert.deepEqual(forward.ranges, [{ start: 1, end: 3 }]);
  assert.equal(forward.copiedText, 'בג');
  // visual rectangle sits over ג(12..18) and ב(18..24)
  assert.deepEqual(forward.rects, [{ line: 0, x: 12, width: 12 }]);

  const backward = selectRange(r, 3, 1); // same range, dragged right-to-left
  assert.equal(backward.backward, true);
  assert.deepEqual(backward.ranges, forward.ranges);
  assert.deepEqual(backward.rects, forward.rects);
  assert.equal(backward.copiedText, 'בג');
});
