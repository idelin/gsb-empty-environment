import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { accessibleName, hitTest, selectRange } from '../src/mapping.ts';
import { metricsV1 } from '../fixtures/metrics.ts';
import { englishParagraph, linkSentence } from '../fixtures/trees.ts';

test('hit test round-trips every visible ltr cluster', () => {
  const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 600, maxLines: 3, mode: 'end' });
  const r = engine.lastLayout;
  for (const row of r.lines) {
    for (const it of row) {
      if (it.kind !== 'cluster') continue;
      assert.equal(hitTest(r, it.line, it.x + 0.5), it.srcStart, `near edge of ${it.text}`);
      assert.equal(hitTest(r, it.line, it.x + it.width - 0.5), it.srcEnd, `far edge of ${it.text}`);
    }
  }
});

test('hit test on the ellipsis yields no source offset', () => {
  const engine = new TruncationEngine(linkSentence, metricsV1, { width: 90, maxLines: 1, mode: 'middle' });
  const r = engine.lastLayout;
  assert.ok(r.ellipsis);
  assert.equal(hitTest(r, r.ellipsis!.line, r.ellipsis!.x + 1), null);
});

test('selection across the ellipsis copies the full original text', () => {
  const engine = new TruncationEngine(linkSentence, metricsV1, { width: 90, maxLines: 1, mode: 'middle' });
  const r = engine.lastLayout;
  const sel = selectRange(r, 0, r.source.length);
  assert.equal(sel.copiedText, r.source); // elided text included in the copy
  assert.ok(sel.ranges.length >= 2, 'visible selection split by the ellipsis');
  assert.equal(sel.backward, false);
});

test('accessible name is always the complete original text', () => {
  const engine = new TruncationEngine(linkSentence, metricsV1, { width: 90, maxLines: 1, mode: 'middle' });
  const r = engine.lastLayout;
  assert.equal(r.truncated, true);
  assert.equal(accessibleName(r), 'Open the complete reference documentation now');
});
