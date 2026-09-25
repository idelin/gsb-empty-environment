import test from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/engine.ts';
import { metricsV1 } from '../fixtures/metrics.ts';
import { englishParagraph, entitySentence } from '../fixtures/trees.ts';
import { allText, lineText, lineWidth } from './helpers.ts';

test('multi-line clamp puts the ellipsis on the last line', () => {
  const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 120, maxLines: 2, mode: 'end' });
  const r = engine.lastLayout;
  assert.equal(r.truncated, true);
  assert.ok(r.lines.length <= 2);
  assert.ok(r.ellipsis, 'ellipsis placed');
  assert.equal(r.ellipsis!.line, r.lines.length - 1);
  for (let i = 0; i < r.lines.length; i++) {
    assert.ok(lineWidth(r, i) <= 120, `line ${i} fits`);
  }
  assert.ok(lineText(r, r.lines.length - 1).endsWith('…'));
});

test('content that fits is not truncated', () => {
  const engine = new TruncationEngine(englishParagraph, metricsV1, { width: 600, maxLines: 2, mode: 'end' });
  const r = engine.lastLayout;
  assert.equal(r.truncated, false);
  assert.equal(r.ellipsis, undefined);
  assert.ok(!allText(r).includes('…'));
});

test('unbreakable entity is never split across lines or cuts', () => {
  // entity (66px) is wider than the column (50px): it must overflow
  // whole on its own line, never break apart.
  const engine = new TruncationEngine(entitySentence, metricsV1, { width: 50, maxLines: 4, mode: 'end' });
  const r = engine.lastLayout;
  const entityLine = r.lines.find((row) => row.some((it) => it.text === '@'));
  assert.ok(entityLine, 'entity visible');
  const entityText = entityLine!.map((it) => it.text).join('');
  assert.equal(entityText, '@AliceSmith');

  // narrow clamp: the entity is dropped entirely, never partially
  const engine2 = new TruncationEngine(entitySentence, metricsV1, { width: 50, maxLines: 1, mode: 'end' });
  const r2 = engine2.lastLayout;
  const texts = r2.lines.flat().map((it) => it.text);
  assert.ok(!texts.includes('@AliceSmith'));
  assert.ok(!texts.some((t) => '@AliceSmith'.startsWith(t) && t.length > 0 && t !== '@AliceSmith' && /@/.test(t)));
});
