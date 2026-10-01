import test from 'node:test';
import assert from 'node:assert/strict';

import { measureText } from '../fixtures/glyph-metrics.ts';
import { buildMainScene } from '../fixtures/scenes.ts';

test('glyph metrics come from the fixture table, not a layout engine', () => {
  // H(722) o(579) m(863) e(561) = 2725 units -> 2725 * 14 / 1000 = 38.15
  assert.equal(measureText('Home', 14), 38.15);
  // L(577) i(239) s(431) t(331) = 1578 units -> 22.09
  assert.equal(measureText('List', 14), 22.09);
  assert.equal(measureText('\u{4e2d}', 14), 7); // fallback advance 500
});

test('scene node sizes are derived from fixture glyph metrics', () => {
  const tree = buildMainScene();
  assert.equal(tree.get('nav-home').rect.w, measureText('Home', 14) + 32);
  assert.equal(tree.get('nav-list').rect.w, measureText('List', 14) + 32);
  assert.equal(tree.get('nav-about').rect.w, measureText('About', 14) + 32);
});
