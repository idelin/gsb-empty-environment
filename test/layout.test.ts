import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { layoutTree, measureText, hitTest } from '../src/layout.ts';
import type { SceneFixture } from '../src/harness.ts';

const fixture = JSON.parse(readFileSync(new URL('../fixtures/scene.json', import.meta.url), 'utf8')) as SceneFixture;

test('text width derives from glyph metrics, not a layout engine', () => {
  // "Click" = C(700) l(236) i(230) c(460) k(490) = 2116 units @16px/1000upm
  const w = measureText('Click', 16, fixture.glyphMetrics);
  assert.ok(Math.abs(w - (2116 / 1000) * 16) < 1e-9);
});

test('layout sizes zero-box text nodes from metrics', () => {
  const laid = layoutTree(fixture.tree, fixture.glyphMetrics);
  const label = laid.children[0]!.children[0]!;
  assert.equal(label.id, 'button-label');
  assert.ok(label.rect.w > 0);
  assert.equal(label.rect.h, 16 * 1.2);
});

test('hitTest returns deepest containing node, misses outside', () => {
  const laid = layoutTree(fixture.tree, fixture.glyphMetrics);
  assert.equal(hitTest(laid, { x: 55, y: 60 })!.id, 'button-label');
  assert.equal(hitTest(laid, { x: 25, y: 25 })!.id, 'button');
  assert.equal(hitTest(laid, { x: 200, y: 150 })!.id, 'canvas');
  assert.equal(hitTest(laid, { x: 999, y: 999 }), null);
});
