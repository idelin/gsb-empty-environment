import test from 'node:test';
import assert from 'node:assert/strict';
import { measureNode, wrapLineCount } from '../src/metrics.ts';
import { fixtureMetrics, imageNode, spacerNode, stackNode, textNode } from '../src/fixtures.ts';

const metrics = fixtureMetrics(); // lineHeight 20, default advance 8, space 4

test('word wrap counts lines from glyph advances only', () => {
  // "aa aa" = 16 + 4 + 16 = 36px
  assert.equal(wrapLineCount('aa aa', 40, metrics), 1);
  assert.equal(wrapLineCount('aa aa', 36, metrics), 1);
  assert.equal(wrapLineCount('aa aa', 35, metrics), 2);
  assert.equal(wrapLineCount('aa aa aa', 40, metrics), 2);
});

test('overlong words break by glyph', () => {
  // 6 chars * 8px = 48px in a 20px box -> 2 chars/line -> 3 lines
  assert.equal(wrapLineCount('aaaaaa', 20, metrics), 3);
});

test('measureNode combines text, images, spacers and stacks', () => {
  assert.equal(measureNode(textNode('aa aa'), 40, metrics), 20);
  assert.equal(measureNode(textNode('aa aa'), 35, metrics), 40);
  assert.equal(measureNode(imageNode(100, 64), 40, metrics), 64);
  assert.equal(measureNode(spacerNode(7), 40, metrics), 7);
  const stack = stackNode([textNode('aa aa'), imageNode(10, 10), spacerNode(5)], 4);
  // 20 + 4 + 10 + 4 + 5
  assert.equal(measureNode(stack, 40, metrics), 43);
});

test('narrower width rewraps text taller', () => {
  const node = textNode('aaaa aaaa aaaa aaaa');
  const wide = measureNode(node, 400, metrics);
  const narrow = measureNode(node, 60, metrics);
  assert.ok(narrow > wide);
});
