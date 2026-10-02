import { test } from 'node:test';
import assert from 'node:assert/strict';
import { measureText, hitTest, textBox } from '../src/layout.ts';
import { GLYPHS, buildFixtureTree } from '../src/fixtures.ts';

test('text measurement comes from the glyph fixture, not a layout engine', () => {
  // 'Pinch' = 650+250+550+500+550 = 2500 units -> 2500 * 16 / 1000 = 40px
  assert.equal(measureText('Pinch', 16, GLYPHS), 40);
  // unknown characters fall back to the fixture fallback advance
  assert.equal(measureText('\u4e2d', 10, GLYPHS), 5);
  const node = textBox('t', 0, 0, 'Tap me', 14, 20, GLYPHS);
  assert.equal(node.h, 20);
  assert.ok(node.w > 0);
});

test('hit test returns the deepest node and respects sibling order', () => {
  const tree = buildFixtureTree();
  assert.equal(hitTest(tree, 45, 178), 'button-label'); // inside the label text box
  assert.equal(hitTest(tree, 30, 165), 'button'); // button padding, outside label
  assert.equal(hitTest(tree, 36, 36), 'card-title');
  assert.equal(hitTest(tree, 5, 5), 'screen'); // background
  assert.equal(hitTest(tree, 500, 900), undefined); // outside the tree
});
