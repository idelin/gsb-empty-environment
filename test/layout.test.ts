import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { buildDocument, flatten } from '../src/engine/model.ts';
import { StyleEngine } from '../src/engine/engine.ts';
import { layoutDocument, measureText, wrapText } from '../src/layout/layout.ts';
import type { GlyphMetrics } from '../src/layout/layout.ts';

const metrics = JSON.parse(readFileSync(new URL('../fixtures/glyphs.json', import.meta.url), 'utf8')) as GlyphMetrics;

test('glyph metrics drive text measurement (16 units = font size)', () => {
  // unitsPerEm = 16, so at font-size 16 each unit is exactly 1px.
  assert.equal(measureText('ab', 16, metrics), 7 + 8);
  assert.equal(measureText('a b', 16, metrics), 7 + 4 + 8);
  assert.equal(measureText('ab', 32, metrics), 2 * (7 + 8));
  assert.equal(measureText('~', 16, metrics), 8); // default advance
});

test('greedy wrapping breaks lines at the configured width', () => {
  // "ab ab" = 15 + 4 + 15 = 34px at font-size 16; width 20 forces two lines.
  const lines = wrapText('ab ab', 16, 20, metrics);
  assert.deepEqual(lines, [
    { text: 'ab', width: 15 },
    { text: 'ab', width: 15 },
  ]);
  // A word longer than the line is hard-split by glyph.
  const hard = wrapText('abcd', 16, 20, metrics);
  assert.deepEqual(hard, [
    { text: 'ab', width: 15 },
    { text: 'cd', width: 15 },
  ]);
});

test('layout stacks blocks and derives heights from font metrics', () => {
  const root = buildDocument({
    tag: 'div', classes: ['page'], children: [
      { tag: 'p', classes: ['line'], text: 'ab ab' },
      { tag: 'p', classes: ['line'], text: 'ab' },
    ],
  });
  const nodes = flatten(root);
  const css = `
    .page { width: 20; padding: 0; margin: 0; font-size: 16; line-height: 1; }
    .line { margin: 1; }
  `;
  const engine = new StyleEngine(root, css);
  const box = layoutDocument(root, engine.getSnapshot(), metrics, 20);
  // Root: width 20, no padding; each child has margin 1 -> content width 18,
  // so "ab ab" (34px) wraps to 2 lines of "ab" (15px each).
  assert.equal(box.width, 20);
  const [first, second] = box.children;
  assert.equal(first.lines.length, 2);
  assert.equal(second.lines.length, 1);
  // Child height = margin*2 + lineCount * 16.
  assert.equal(first.height, 2 + 2 * 16);
  assert.equal(second.height, 2 + 1 * 16);
  // Root height = padding*2 + children.
  assert.equal(box.height, first.height + second.height);
  assert.equal(second.y, first.y + first.height);
});

test('display:none removes a subtree from layout', () => {
  const root = buildDocument({
    tag: 'div', children: [
      { tag: 'p', classes: ['gone'], text: 'ab' },
      { tag: 'p', text: 'ab' },
    ],
  });
  const engine = new StyleEngine(root, '.gone { display: none; } p { margin: 0; } div { line-height: 1; }');
  const box = layoutDocument(root, engine.getSnapshot(), metrics, 100);
  assert.equal(box.children[0].height, 0);
  assert.equal(box.children[1].height, 16);
  assert.equal(box.height, 16);
});
