import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TruncationEngine } from '../src/index.ts';
import {
  metricsV1, metricsWideEllipsis, longText, mixedBidi, withEntity, emojiText, conflictText,
} from '../fixtures/fixtures.ts';

const lineText = (eng: TruncationEngine, li: number) =>
  eng.current!.result.lines[li].items
    .map((i) => (i.kind === 'atom' ? i.atom.text : '…')).join('');

test('多行 + 末尾省略:行数受限,省略号落在末行', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(longText, { lineWidth: 20, maxLines: 2, lineHeight: 1, mode: 'end' });
  assert.equal(r.truncated, true);
  assert.equal(r.lines.length, 2);
  const last = r.lines[1];
  assert.equal(last.items[last.items.length - 1].kind, 'ellipsis');
  assert.ok(last.width <= 20);
});

test('内容放得下时不截断、无省略号', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(longText, { lineWidth: 200, maxLines: 2, lineHeight: 1 });
  assert.equal(r.truncated, false);
  assert.ok(r.lines.every((l) => l.items.every((i) => i.kind === 'atom')));
});

test('中间省略 + 关键片段保留:lazy 完整可见', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(longText, {
    lineWidth: 20, maxLines: 2, lineHeight: 1, mode: 'middle',
    keyFragments: [{ id: 'lazy', start: 35, end: 39 }],
  });
  assert.equal(r.conflicts.length, 0);
  const last = r.lines[1];
  const keptText = last.items.filter((i) => i.kind === 'atom').map((i) => i.kind === 'atom' ? i.atom.text : '').join('');
  assert.ok(keptText.includes('lazy'), `expected 'lazy' kept, got ${keptText}`);
  assert.ok(last.items.some((i) => i.kind === 'ellipsis'));
  assert.ok(last.width <= 20);
});

test('关键片段互相超容量:输出明确冲突', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(conflictText, {
    lineWidth: 20, maxLines: 1, lineHeight: 1, mode: 'middle',
    keyFragments: [
      { id: 'head', start: 0, end: 10 },
      { id: 'tail', start: 44, end: 54 },
    ],
  });
  assert.equal(r.conflicts.length, 1);
  const c = r.conflicts[0];
  assert.equal(c.type, 'key-fragment-over-capacity');
  assert.deepEqual(c.fragments.map((f) => f.id), ['head', 'tail']);
  assert.ok(c.requiredWidth > c.availableWidth);
});

test('省略号本身超宽:明确退化,布局仍产出', () => {
  const eng = new TruncationEngine(metricsWideEllipsis);
  const r = eng.layout(longText, { lineWidth: 20, maxLines: 1, lineHeight: 1, mode: 'end' });
  assert.equal(r.degradations.length, 1);
  assert.equal(r.degradations[0].type, 'ellipsis-too-wide');
  assert.equal(r.degradations[0].ellipsisWidth, 30);
  assert.equal(r.lines.length, 1);
  assert.ok(r.lines[0].items.every((i) => i.kind === 'atom'));
});

test('实体不可拆:整体换行、永不从中间截断', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(withEntity, { lineWidth: 12, maxLines: 5, lineHeight: 1, mode: 'end' });
  const entityLine = r.lines.find((l) =>
    l.items.some((i) => i.kind === 'atom' && i.atom.kind === 'entity'));
  assert.ok(entityLine, 'entity should be visible');
  const item = entityLine.items.find((i) => i.kind === 'atom' && i.atom.kind === 'entity');
  assert.ok(item && item.kind === 'atom' && item.atom.text === '@channel');
});

test('emoji 组合簇不被折行或省略切开', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(emojiText, { lineWidth: 5, maxLines: 4, lineHeight: 1, mode: 'end' });
  const texts = r.lines.flatMap((l) =>
    l.items.filter((i) => i.kind === 'atom').map((i) => i.kind === 'atom' ? i.atom.text : ''));
  assert.ok(texts.includes('👨‍👩‍👧'), 'family emoji kept whole');
  assert.ok(texts.includes('🇨🇳'));
  assert.ok(texts.includes('👍🏽'));
  // 每个可见原子都是完整簇
  for (const t of texts) assert.ok(typeof t === 'string' && t.length > 0);
});

test('截断后可访问名称仍是完整原文', () => {
  const eng = new TruncationEngine(metricsV1);
  eng.layout(longText, { lineWidth: 10, maxLines: 1, lineHeight: 1, mode: 'end' });
  assert.equal(
    eng.current!.accessibleName,
    'the quick brown fox jumps over the lazy dog again and again',
  );
});

test('混合方向片段:RTL run 视觉反转', () => {
  const eng = new TruncationEngine(metricsV1);
  const r = eng.layout(mixedBidi, { lineWidth: 40, maxLines: 3, lineHeight: 1 });
  assert.equal(r.truncated, false);
  const visual = r.lines[0].items.map((i) => (i.kind === 'atom' ? i.atom.text : '')).join('');
  assert.equal(visual, 'hello םולש world');
});
