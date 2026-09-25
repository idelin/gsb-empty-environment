import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TreeController } from '../src/controller.ts';
import { measureText, rowGeometry, contentWidth } from '../src/measure.ts';
import { fixtureGlyphs } from '../fixtures/glyphs.fixture.ts';
import { fixtureTree } from '../fixtures/tree.fixture.ts';

test('measureText sums fixture advances with default fallback', () => {
  // a=6 l=2 p=6 h=6 a=6
  assert.equal(measureText('alpha', fixtureGlyphs), 26);
  // 'z' is not in the fixture -> defaultAdvance 6
  assert.equal(measureText('z', fixtureGlyphs), 6);
  assert.equal(measureText('a z', fixtureGlyphs), 6 + 3 + 6);
});

test('rowGeometry applies indentation per depth', () => {
  const g = rowGeometry('x', 2, 'alpha', fixtureGlyphs, 10);
  assert.equal(g.x, 20);
  assert.equal(g.width, 20 + 26);
});

test('contentWidth tracks the widest visible row without any DOM', () => {
  const c = new TreeController(fixtureTree);
  c.expand('alpha');
  const w1 = c.contentWidth(fixtureGlyphs, 16);
  const geoms = c.geometry(fixtureGlyphs, 16);
  assert.equal(w1, contentWidth(geoms));
  // Widest row is a depth-1 child: 16 + label width.
  const expected = Math.max(...geoms.map((g) => g.width));
  assert.equal(w1, expected);
  c.collapse('alpha');
  assert.ok(c.contentWidth(fixtureGlyphs, 16) < w1, 'narrower once children hide');
});
