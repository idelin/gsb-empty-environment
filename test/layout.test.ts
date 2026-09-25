import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { KeyframeAnimation } from '../src/animation.ts';
import { applyProps, layout, measureText } from '../src/layout.ts';
import type { GlyphMetrics, LayoutNode } from '../src/layout.ts';

const metrics = JSON.parse(
  readFileSync(new URL('../fixtures/glyph-metrics.json', import.meta.url), 'utf8'),
) as GlyphMetrics;
const nodeTree = JSON.parse(
  readFileSync(new URL('../fixtures/node-tree.json', import.meta.url), 'utf8'),
) as LayoutNode;

test('text measurement uses fixture glyph advances, no layout engine', () => {
  assert.equal(measureText('alpha', 10, metrics), 22);
  assert.equal(measureText('beta', 10, metrics), 18);
  assert.equal(measureText('gamma', 10, metrics), 31);
  assert.equal(measureText('ok', 10, metrics), 10);
  assert.equal(measureText('alpha beta', 10, metrics), 42.5);
});

test('fixture node tree lays out deterministically', () => {
  const box = layout(nodeTree, metrics);
  assert.equal(box.width, 120);
  assert.equal(box.height, 58);
  const [title, panel] = box.children!;
  assert.deepEqual(
    { x: title.x, y: title.y, width: title.width, height: title.height },
    { x: 10, y: 10, width: 100, height: 12 },
  );
  assert.equal(title.lines!.length, 1);
  assert.equal(title.lines![0].text, 'alpha beta gamma');
  assert.equal(title.lines![0].width, 76);
  assert.deepEqual(
    { x: panel.x, y: panel.y, width: panel.width, height: panel.height },
    { x: 15, y: 27, width: 90, height: 16 },
  );
  const label = panel.children![0];
  assert.deepEqual({ x: label.x, y: label.y }, { x: 17, y: 29 });
});

test('animated props reflow text wrapping without a browser', () => {
  const widthAnim = new KeyframeAnimation({
    keyframes: [
      { offset: 0, props: { 'root.width': 120 } },
      { offset: 1, props: { 'root.width': 60 } },
    ],
    duration: 1,
  });
  const lineCountAt = (t: number) =>
    layout(applyProps(nodeTree, widthAnim.valueAt(t)), metrics).children![0].lines!.length;

  assert.equal(lineCountAt(0), 1); // width 120, content 100, text 76 fits
  assert.equal(lineCountAt(0.5), 2); // width 90, content 70: 'alpha beta' + 'gamma'
  assert.equal(lineCountAt(1), 3); // width 60, content 40: one word per line

  const shrunk = layout(applyProps(nodeTree, widthAnim.valueAt(1)), metrics);
  assert.equal(shrunk.height, 82);
});
