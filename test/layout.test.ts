import { test } from 'node:test';
import assert from 'node:assert/strict';
import { layout, measureText, wrapText } from '../src/layout.ts';
import { glyphFixture } from '../fixtures/glyphs.ts';
import { treeFixture } from '../fixtures/tree.ts';
import { mulberry32, intBetween, pick } from './rng.util.ts';

test('text measurement sums fixture glyph advances', () => {
  assert.equal(measureText('Hello', glyphFixture), 8 + 8 + 3 + 3 + 8);
  assert.equal(measureText('Hello world', glyphFixture), 72);
  assert.equal(measureText('', glyphFixture), 0);
});

test('greedy wrapping follows the metrics', () => {
  assert.deepStrictEqual(wrapText('Hello world', 48, glyphFixture), ['Hello', 'world']);
  assert.deepStrictEqual(wrapText('Hello world', 80, glyphFixture), ['Hello world']);
  // A word wider than the line is hard-broken by characters.
  assert.deepStrictEqual(wrapText('mmmmmm', 30, glyphFixture), ['mm', 'mm', 'mm']);
});

test('box tree layout is computed purely from fixtures', () => {
  const box = layout(treeFixture, 100, glyphFixture);
  assert.equal(box.width, 100);
  assert.equal(box.height, 132);
  const [title, card] = box.children ?? [];
  assert.deepStrictEqual(
    { x: title.x, y: title.y, width: title.width, height: title.height },
    { x: 10, y: 10, width: 80, height: 16 },
  );
  assert.deepStrictEqual(
    title.lines?.map((l) => [l.text, l.y, l.width]),
    [['Hello world', 10, 72]],
  );
  assert.deepStrictEqual(
    { x: card.x, y: card.y, width: card.width, height: card.height },
    { x: 10, y: 32, width: 80, height: 90 },
  );
  const [body, foot] = card.children ?? [];
  assert.deepStrictEqual(
    body.lines?.map((l) => l.text),
    ['Nested', 'content', 'here'],
  );
  assert.deepStrictEqual(
    foot.lines?.map((l) => [l.text, l.y]),
    [
      ['Second', 86],
      ['line', 102],
    ],
  );
});

test('generated strings: measurement is additive and wrapping fits', () => {
  const rng = mulberry32(99);
  const alphabet = ['a', 'b', 'i', ' ', '.'] as const;
  for (let trial = 0; trial < 300; trial++) {
    const lenA = intBetween(rng, 0, 20);
    const lenB = intBetween(rng, 0, 20);
    const a = Array.from({ length: lenA }, () => pick(rng, alphabet)).join('');
    const b = Array.from({ length: lenB }, () => pick(rng, alphabet)).join('');
    assert.equal(
      measureText(a + b, glyphFixture),
      measureText(a, glyphFixture) + measureText(b, glyphFixture),
    );
  }
  // Words that individually fit never overflow their line.
  for (let trial = 0; trial < 300; trial++) {
    const words = Array.from({ length: intBetween(rng, 1, 8) }, () =>
      Array.from({ length: intBetween(rng, 1, 5) }, () => pick(rng, ['a', 'b', 'i'] as const)).join(''),
    );
    const text = words.join(' ');
    const lines = wrapText(text, 60, glyphFixture);
    for (const line of lines) {
      assert.ok(measureText(line, glyphFixture) <= 60, `line too wide: "${line}"`);
    }
    assert.equal(lines.join(' '), text);
  }
});
