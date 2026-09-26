import { test } from 'node:test';
import assert from 'node:assert/strict';
import { breakParagraph } from '../src/linebreak.ts';
import type { GlyphMetrics } from '../src/types.ts';

const metrics: GlyphMetrics = {
  charWidths: {},
  defaultCharWidth: 10,
  spaceWidth: 5,
  lineHeight: 10,
  headingHeight: 10,
  maxLineWidth: 100,
};

test('greedy line breaking uses fixture glyph metrics', () => {
  const { lines } = breakParagraph('aaaa bbbb cccc', metrics, 100);
  assert.deepEqual(lines.map((l) => l.text), ['aaaa bbbb', 'cccc']);
  assert.equal(lines[0].width, 85);
});

test('footnote markers are zero-width and attributed to their line', () => {
  const { lines } = breakParagraph('aaaa[^f1] bbbb cccc', metrics, 100);
  assert.deepEqual(lines.map((l) => l.text), ['aaaa bbbb', 'cccc']);
  assert.deepEqual(lines[0].footnoteRefs, ['f1']);
  assert.deepEqual(lines[1].footnoteRefs, []);
});

test('overlong words are reported, not silently clipped', () => {
  const { lines, overflowWords } = breakParagraph('aaaaaaaaaaaaaaaaaaaaa bb', metrics, 100);
  assert.deepEqual(overflowWords, ['aaaaaaaaaaaaaaaaaaaaa']);
  assert.equal(lines.length, 2);
});
