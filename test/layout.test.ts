import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  layoutParagraph,
  layoutDocument,
  cursorRect,
  textWidth,
} from '../src/layout.ts';
import { glyphMetrics, makeDocumentFixture } from '../src/fixtures.ts';

test('greedy wrap breaks at spaces and respects max width', () => {
  const lines = layoutParagraph(glyphMetrics, 'p', 'hello world', 40, 0);
  assert.equal(lines.length, 2);
  assert.deepEqual(lines.map((l) => 'hello world'.slice(l.start, l.end)), ['hello', 'world']);
  for (const l of lines) assert.ok(l.width <= 40);
  assert.equal(lines[1].y, glyphMetrics.lineHeight);
});

test('long words hard-break when no space is available', () => {
  const lines = layoutParagraph(glyphMetrics, 'p', 'aaaaaaaaaa', 30, 0);
  assert.ok(lines.length > 1);
  for (const l of lines) assert.ok(l.width <= 30);
  // concatenation of lines reproduces the original text
  assert.equal(lines.map((l) => 'aaaaaaaaaa'.slice(l.start, l.end)).join(''), 'aaaaaaaaaa');
});

test('document layout stacks paragraphs vertically', () => {
  const tree = makeDocumentFixture();
  const layout = layoutDocument(glyphMetrics, tree, 120);
  const p1 = layout.lines.filter((l) => l.nodeId === 'p1');
  const p2 = layout.lines.filter((l) => l.nodeId === 'p2');
  assert.ok(p2[0].y >= p1[p1.length - 1].y + glyphMetrics.lineHeight);
  assert.equal(layout.height, layout.lines.length * glyphMetrics.lineHeight);
});

test('cursor geometry is derived from layout, not a browser', () => {
  const text = 'hello world';
  const layout = layoutDocument(glyphMetrics, makeDocumentFixture(), 120);
  const rect = cursorRect(glyphMetrics, layout, 'p1', text, 5);
  assert.equal(rect.x, textWidth(glyphMetrics, 'hello'));
  assert.equal(rect.y, 0);
});

test('layout is deterministic', () => {
  const tree = makeDocumentFixture();
  const a = layoutDocument(glyphMetrics, tree, 47);
  const b = layoutDocument(glyphMetrics, tree, 47);
  assert.deepEqual(a, b);
});
