import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { metricsV1, metricsWideEllipsis } from '../fixtures/metrics.ts';
import { englishParagraph } from '../fixtures/trees.ts';
import { allText } from './helpers.ts';

test('ellipsis wider than the line degrades explicitly', () => {
  const engine = new TruncationEngine(englishParagraph, metricsWideEllipsis, { width: 40, maxLines: 2, mode: 'end' });
  const r = engine.lastLayout;
  assert.equal(r.truncated, true);
  assert.ok(r.degraded, 'degradation reported');
  assert.equal(r.degraded!.reason, 'ellipsis-too-wide');
  assert.equal(r.degraded!.ellipsisWidth, 50);
  assert.equal(r.degraded!.lineWidth, 40);
  assert.ok(!allText(r).includes('…'), 'no ellipsis glyph placed');
  assert.ok(r.lines.length <= 2);
});

test('same content with a sane ellipsis truncates normally', () => {
  const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 40, maxLines: 2, mode: 'end' });
  const r = engine.lastLayout;
  assert.equal(r.truncated, true);
  assert.equal(r.degraded, undefined);
  assert.ok(allText(r).includes('…'));
});
