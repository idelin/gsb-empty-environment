import test from 'node:test';
import assert from 'node:assert/strict';

import { FocusController } from '../src/controller.ts';
import { keyEvent, replay, type ReplayResult } from '../src/replay.ts';
import { LayoutTree } from '../src/tree.ts';
import { buildMainScene, buildNestedScrollScene } from '../fixtures/scenes.ts';

function assertNoFocusLoss(tree: LayoutTree, result: ReplayResult): void {
  for (const record of result.records) {
    if (record.step.type !== 'settle') continue;
    if (record.anyFocusable) {
      assert.ok(record.focus, 'focus lost after settle');
      assert.ok(record.focusValid, `focus on invalid node ${record.focus}`);
    }
  }
}

test('replay: mixed journey has no focus loss and stays in sync with the announcer', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  const result = await replay(tree, controller, [
    { type: 'settle' }, // initial recovery -> nav-home
    { type: 'key', event: keyEvent('ArrowDown') }, // nav-list
    { type: 'key', event: keyEvent('ArrowDown') }, // nav-about
    { type: 'key', event: keyEvent('ArrowDown') }, // card-2 (main-axis priority)
    { type: 'delete', id: 'card-2' }, // focused node deleted
    { type: 'settle' }, // recovery -> nav-home
    { type: 'key', event: keyEvent('ArrowRight') }, // card-1
    { type: 'press', event: keyEvent('ArrowDown') }, // card-3
    { type: 'press', event: keyEvent('ArrowDown') }, // plans card-4 (slow scroll)
    { type: 'press', event: keyEvent('ArrowRight') }, // wrap -> nav-list, expires the scroll
    { type: 'settle' },
    { type: 'collapse', id: 'main' }, // focus (nav-list) unaffected
    { type: 'key', event: keyEvent('ArrowDown') }, // nav-about
    { type: 'collapse', id: 'sidebar' }, // everything focusable is now gone
    { type: 'settle' }, // focus becomes null, no crash
    { type: 'collapse', id: 'sidebar', collapsed: false },
    { type: 'collapse', id: 'main', collapsed: false },
    { type: 'settle' }, // recovery -> nav-home
  ]);

  assertNoFocusLoss(tree, result);
  assert.equal(result.finalFocus, 'nav-home');

  // Screen-reader sync: announcements are sequential and end on the final focus.
  const log = controller.announcer.log;
  assert.deepEqual(log.map((a) => a.seq), log.map((_, i) => i + 1));
  assert.equal(log.at(-1)?.id, result.finalFocus);
  // The expired slow scroll never announced card-4; the deleted node was never announced.
  assert.ok(!log.some((a) => a.id === 'card-4'));
  assert.ok(!log.some((a) => a.id === 'card-2' && a.reason !== 'directional'));
});

test('replay: entering a modal traps focus until it closes', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  const result = await replay(tree, controller, [
    { type: 'settle' }, // nav-home
    { type: 'key', event: keyEvent('ArrowRight') }, // card-1
    { type: 'key', event: keyEvent('ArrowDown') }, // card-2
    { type: 'key', event: keyEvent('ArrowDown') }, // card-3
    { type: 'collapse', id: 'dialog', collapsed: false }, // open the modal
    { type: 'key', event: keyEvent('ArrowDown') }, // enters modal -> dlg-ok
    { type: 'key', event: keyEvent('ArrowLeft') }, // wraps inside -> dlg-cancel
    { type: 'key', event: keyEvent('ArrowRight') }, // wraps inside -> dlg-ok
    { type: 'key', event: keyEvent('ArrowUp') }, // stays inside
    { type: 'key', event: keyEvent('ArrowDown') }, // stays inside
  ]);
  const modalFocuses = result.records.slice(5).map((r) => r.focus);
  assert.ok(modalFocuses.every((id) => id === 'dlg-ok' || id === 'dlg-cancel'));
  // The occluded secret button is never focused or announced.
  assert.ok(!controller.announcer.log.some((a) => a.id === 'dlg-secret'));
});

test('replay: two rapid presses expire the first slow mount/scroll', async () => {
  const tree = buildMainScene();
  const controller = new FocusController(tree);
  const result = await replay(tree, controller, [
    { type: 'settle' }, // nav-home
    { type: 'key', event: keyEvent('ArrowRight') }, // card-1
    { type: 'key', event: keyEvent('ArrowDown') }, // card-2
    { type: 'key', event: keyEvent('ArrowDown') }, // card-3
    { type: 'press', event: keyEvent('ArrowDown') }, // plans card-4, slow scroll
    { type: 'press', event: keyEvent('ArrowRight') }, // wrap -> nav-list, supersedes
    { type: 'settle' }, // slow scroll completes; result is stale and discarded
  ]);
  assert.equal(result.finalFocus, 'nav-list');
  assert.ok(!controller.announcer.log.some((a) => a.id === 'card-4'));
});

test('replay: nested scroll journey converges without cycles', async () => {
  const tree = buildNestedScrollScene();
  const controller = new FocusController(tree);
  const result = await replay(tree, controller, [
    { type: 'settle' }, // header
    { type: 'press', event: keyEvent('ArrowDown') },
    { type: 'settle' }, // item-a
    { type: 'press', event: keyEvent('ArrowDown') },
    { type: 'settle' }, // item-b
    { type: 'press', event: keyEvent('ArrowDown') },
    { type: 'settle' }, // item-f
  ]);
  assert.equal(result.finalFocus, 'item-f');
  assertNoFocusLoss(tree, result);
});
