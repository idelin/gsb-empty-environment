import test from 'node:test';
import assert from 'node:assert/strict';

import { FocusController } from '../src/controller.ts';
import { buildColumnScene, buildMainScene, buildNestedScrollScene } from '../fixtures/scenes.ts';

test('immediate directional move commits focus and announces it', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  controller.setFocus('card-1');
  const res = await controller.handleKey('down');
  assert.equal(res.status, 'focused');
  assert.equal(res.target, 'card-2');
  assert.equal(controller.focusId, 'card-2');
  assert.equal(controller.announcer.log.at(-1)?.id, 'card-2');
});

test('out-of-view target is scrolled into view before focus lands', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  controller.setFocus('card-3');
  const pending = controller.handleKey('down');
  await tree.completeAllPending();
  const res = await pending;
  assert.equal(res.target, 'card-4');
  assert.equal(tree.get('main').scrollOffset.y, 200);
  assert.ok(tree.isFocusableNow('card-4'));
});

test('unmounted target is mounted and scrolled, then focused', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  controller.setFocus('card-3');
  const first = controller.handleKey('down');
  await tree.completeAllPending();
  await first; // focus now on card-4
  const second = controller.handleKey('down');
  await tree.completeAllPending();
  const res = await second;
  assert.equal(res.target, 'card-5');
  assert.equal(tree.get('lazy-host').mountState, 'mounted');
  assert.ok(tree.isFocusableNow('card-5'));
});

test('a second key press expires the first slow result', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  controller.setFocus('card-3');
  const slow = controller.handleKey('down'); // card-4 needs a scroll: slow
  const fast = controller.handleKey('right'); // wraps to nav-list: immediate
  await tree.completeAllPending();
  const [slowRes, fastRes] = await Promise.all([slow, fast]);
  assert.equal(slowRes.status, 'stale');
  assert.equal(fastRes.status, 'focused');
  assert.equal(controller.focusId, 'nav-list');
  assert.ok(!controller.announcer.log.some((a) => a.id === 'card-4'));
});

test('candidate deleted mid-flight triggers a re-plan, never focuses the dead node', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  controller.setFocus('card-3');
  const pending = controller.handleKey('down'); // plans card-4, requests scroll
  tree.delete('card-4');
  for (let i = 0; i < 4; i += 1) await tree.completeAllPending();
  const res = await pending;
  assert.equal(res.status, 'focused');
  assert.equal(res.target, 'card-5'); // re-planned to the lazy card
  assert.ok(!controller.announcer.log.some((a) => a.id === 'card-4'));
  assert.ok(tree.isFocusableNow(controller.focusId!));
});

test('geometry changed during scroll: target re-validated against new layout', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  controller.setFocus('card-3');
  const pending = controller.handleKey('down'); // plans card-4 at y=700
  tree.setRect('card-4', { x: 210, y: 1200, w: 380, h: 100 }); // moves mid-flight
  for (let i = 0; i < 4; i += 1) await tree.completeAllPending();
  const res = await pending;
  // The stale scroll (computed for the old rect) leaves card-4 off-screen,
  // so the controller re-plans instead of focusing an invisible node.
  assert.equal(res.target, 'card-5');
  assert.ok(tree.isFocusableNow('card-5'));
  assert.ok(!controller.announcer.log.some((a) => a.id === 'card-4'));
});

test('current focus deleted: deterministic recovery to logical fallback', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  controller.setFocus('card-2');
  tree.delete('card-2');
  controller.ensureFocus();
  assert.equal(controller.focusId, 'nav-home');
  assert.equal(controller.announcer.log.at(-1)?.reason, 'recovery');
});

test('container collapsed: focus falls back out of the collapsed subtree', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  controller.setFocus('card-2');
  tree.setCollapsed('main', true);
  controller.ensureFocus();
  assert.equal(controller.focusId, 'nav-home');
  assert.ok(tree.isFocusableNow('nav-home'));
});

test('no eligible node: noop, focus stays put, no waiting', async () => {
  const tree = buildColumnScene();
  const controller = new FocusController(tree);
  tree.delete('c2');
  tree.delete('c3');
  controller.setFocus('c1');
  const res = await controller.handleKey('down');
  assert.equal(res.status, 'noop');
  assert.equal(controller.focusId, 'c1');
});

test('nested scrollers: inner and outer offsets are both applied', async () => {
  const tree = buildNestedScrollScene();
  const controller = new FocusController(tree);
  controller.setFocus('header');
  const press = async (): Promise<void> => {
    const p = controller.handleKey('down');
    await tree.completeAllPending();
    await p;
  };
  await press(); // header -> item-a (outer scroll)
  assert.equal(controller.focusId, 'item-a');
  await press(); // item-a -> item-b
  assert.equal(controller.focusId, 'item-b');
  await press(); // item-b -> item-f (inner + outer scroll)
  assert.equal(controller.focusId, 'item-f');
  assert.equal(tree.get('page').scrollOffset.y, 450);
  assert.equal(tree.get('list').scrollOffset.y, 150);
  assert.ok(tree.isFocusableNow('item-f'));
});
