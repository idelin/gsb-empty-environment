import test from 'node:test';
import assert from 'node:assert/strict';

import { planNext } from '../src/navigator.ts';
import {
  buildColumnScene,
  buildDiagonalScene,
  buildMainScene,
  buildWrapScene,
} from '../fixtures/scenes.ts';

test('diagonal candidates follow the public main-axis priority rule', () => {
  const tree = buildDiagonalScene();
  // diag-near: main 40 + penalty 2*60 = 160; axis-far: main 200. Main-axis proximity wins.
  const plan = planNext(tree, 'origin', 'right');
  assert.equal(plan?.target, 'diag-near');
  assert.equal(plan?.reason, 'directional');
  // Same rule on the vertical axis: diag-near is 60 down, axis-far is not below.
  const down = planNext(tree, 'origin', 'down');
  assert.equal(down?.target, 'diag-near');
});

test('wrap-around jumps to the extreme opposite edge', () => {
  const tree = buildWrapScene();
  assert.equal(planNext(tree, 'b3', 'right')?.target, 'b1');
  assert.equal(planNext(tree, 'b1', 'left')?.target, 'b3');
  assert.equal(planNext(tree, 'b3', 'right')?.reason, 'wrap');
});

test('legal fallback uses logical order when wrap is disabled', () => {
  const tree = buildColumnScene();
  const plan = planNext(tree, 'c1', 'right', { wrap: false });
  assert.equal(plan?.target, 'c2');
  assert.equal(plan?.reason, 'fallback');
});

test('modal scope cannot be escaped', () => {
  const tree = buildMainScene();
  tree.setCollapsed('dialog', false);
  for (const dir of ['left', 'right', 'up', 'down'] as const) {
    const plan = planNext(tree, 'dlg-ok', dir);
    assert.ok(plan, `expected a plan for ${dir}`);
    assert.ok(plan.target.startsWith('dlg-'), `focus escaped modal via ${dir}: ${plan.target}`);
  }
});

test('occluded nodes are never candidates', () => {
  const tree = buildMainScene();
  tree.setCollapsed('dialog', false);
  // dlg-secret sits between dlg-ok and dlg-cancel horizontally but is occluded.
  const plan = planNext(tree, 'dlg-cancel', 'left');
  assert.equal(plan?.target, 'dlg-ok');
  assert.equal(tree.isEligible('dlg-secret'), false);
});

test('collapsed containers and unmounted subtrees', () => {
  const tree = buildMainScene();
  // Collapsed dialog buttons are not eligible...
  assert.equal(tree.isEligible('dlg-ok'), false);
  // ...while an unmounted lazy node is eligible (it can be mounted on demand).
  assert.equal(tree.isEligible('card-5'), true);
  const plan = planNext(tree, 'card-4', 'down');
  assert.equal(plan?.target, 'card-5');
  assert.deepEqual(plan?.mountTargets, ['lazy-host']);
  assert.equal(plan?.needsScroll, true);
});
