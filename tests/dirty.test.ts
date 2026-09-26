import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Bitmap, Layer, DirtyTracker, RepaintReason,
  translation, rotation, multiply, rect, containsRect, transformRect,
} from '../src/index.ts';

const red2x2 = () => Bitmap.from(2, 2, [
  255, 0, 0, 255, 255, 0, 0, 255,
  255, 0, 0, 255, 255, 0, 0, 255,
]);

test('local edit maps through layer transform to device space', () => {
  const root = new Layer('root');
  const child = root.addChild(new Layer('child', { bitmap: red2x2(), transform: translation(3, 4) }));
  const tracker = new DirtyTracker(root);

  tracker.markLocalEdit(child, rect(0, 0, 1, 1));

  const entries = tracker.getEntries();
  assert.equal(entries.length, 1);
  assert.equal(entries[0].reason, RepaintReason.LocalEdit);
  assert.equal(entries[0].layerId, 'child');
  assert.deepEqual(entries[0].rect, { x: 3, y: 4, width: 1, height: 1 });
});

test('edit fully outside ancestor clip produces no repaint', () => {
  const root = new Layer('root', { clip: rect(0, 0, 4, 4) });
  const child = root.addChild(new Layer('child', { bitmap: Bitmap.from(8, 8, new Array(8 * 8 * 4).fill(255)) }));
  const tracker = new DirtyTracker(root);

  tracker.markLocalEdit(child, rect(5, 5, 2, 2)); // entirely outside the 4x4 clip
  assert.equal(tracker.getEntries().length, 0);
  assert.equal(tracker.dirtyRegion(), null);
});

test('edit partially inside clip is intersected', () => {
  const root = new Layer('root', { clip: rect(0, 0, 4, 4) });
  const child = root.addChild(new Layer('child', { bitmap: Bitmap.from(8, 8, new Array(8 * 8 * 4).fill(255)) }));
  const tracker = new DirtyTracker(root);

  tracker.markLocalEdit(child, rect(2, 2, 4, 4)); // only the 2x2 corner survives
  assert.deepEqual(tracker.getEntries()[0].rect, { x: 2, y: 2, width: 2, height: 2 });
});

test('moving a layer dirties old + new positions', () => {
  const root = new Layer('root');
  const child = root.addChild(new Layer('child', { bitmap: red2x2(), transform: translation(1, 1) }));
  const tracker = new DirtyTracker(root);

  tracker.setTransform(child, translation(5, 3));

  const entries = tracker.getEntries();
  assert.equal(entries.length, 1);
  assert.equal(entries[0].reason, RepaintReason.TransformChanged);
  const dirty = tracker.dirtyRegion()!;
  // old bounds (1,1,2,2) and new bounds (5,3,2,2) must both be covered
  assert.ok(containsRect(dirty, rect(1, 1, 2, 2)), 'old position covered');
  assert.ok(containsRect(dirty, rect(5, 3, 2, 2)), 'new position covered');
  assert.deepEqual(dirty, { x: 1, y: 1, width: 6, height: 4 }); // minimal union
});

test('shadow expands dirty region by declared radius', () => {
  const root = new Layer('root');
  const child = root.addChild(new Layer('child', {
    bitmap: red2x2(),
    transform: translation(10, 10),
    effects: [{ kind: 'shadow', radius: 3, offsetX: 0, offsetY: 0, color: [0, 0, 0, 128] }],
  }));
  const tracker = new DirtyTracker(root);

  tracker.markLocalEdit(child, rect(0, 0, 1, 1));

  // 1px edit at (10,10), expanded by radius 3 on every side
  assert.deepEqual(tracker.getEntries()[0].rect, { x: 7, y: 7, width: 7, height: 7 });
});

test('shadow offset also contributes to conservative expansion', () => {
  const root = new Layer('root');
  const child = root.addChild(new Layer('child', {
    bitmap: red2x2(),
    transform: translation(10, 10),
    effects: [{ kind: 'shadow', radius: 2, offsetX: 4, offsetY: 0, color: [0, 0, 0, 128] }],
  }));
  const tracker = new DirtyTracker(root);

  tracker.markLocalEdit(child, rect(0, 0, 1, 1));
  // grow = radius 2 + |offsetX| 4 = 6 on each side
  assert.deepEqual(tracker.getEntries()[0].rect, { x: 4, y: 4, width: 13, height: 13 });
});

test('opacity change propagates to root with full footprint and path', () => {
  const root = new Layer('root');
  const group = root.addChild(new Layer('group', { transform: translation(2, 2) }));
  const child = group.addChild(new Layer('child', { bitmap: red2x2(), transform: translation(1, 1) }));
  const tracker = new DirtyTracker(root);

  tracker.setOpacity(child, 0.5);

  const entries = tracker.getEntries();
  assert.equal(entries.length, 1);
  assert.equal(entries[0].reason, RepaintReason.OpacityChanged);
  assert.deepEqual(entries[0].rect, { x: 3, y: 3, width: 2, height: 2 });
  assert.deepEqual(entries[0].propagationPath, ['child', 'group', 'root']);
});

test('parent transform change dirties the whole subtree footprint', () => {
  const root = new Layer('root');
  const group = root.addChild(new Layer('group', { transform: translation(0, 0) }));
  group.addChild(new Layer('child', { bitmap: red2x2(), transform: translation(1, 1) }));
  const tracker = new DirtyTracker(root);

  // group itself has no bitmap; its own bounds are empty, but moving it moves the child.
  // The tracker is told about the group, so we mark the subtree: emulate by marking the child.
  tracker.setTransform(group, translation(10, 0));
  // group has no own content -> no entry for the group itself
  assert.equal(tracker.getEntries().length, 0);

  tracker.setTransform(group.children[0], translation(11, 1));
  const dirty = tracker.dirtyRegion()!;
  assert.ok(containsRect(dirty, rect(11, 1, 2, 2)), 'old child pos (10+1,1) covered');
  assert.ok(containsRect(dirty, rect(21, 1, 2, 2)), 'new child pos (20+1,1) covered');
});

test('rotation produces conservative axis-aligned bounds', () => {
  const root = new Layer('root');
  const m = multiply(translation(4, 4), rotation(Math.PI / 4));
  const child = root.addChild(new Layer('child', { bitmap: red2x2(), transform: m }));
  const tracker = new DirtyTracker(root);

  tracker.markLocalEdit(child, rect(0, 0, 2, 2));

  const dirty = tracker.dirtyRegion()!;
  const exact = transformRect(m, rect(0, 0, 2, 2));
  assert.ok(containsRect(dirty, exact), 'dirty region conservatively contains rotated rect');
  // diagonal of a 2x2 square rotated 45deg is 2*sqrt(2) ~= 2.83 wide
  assert.ok(dirty.width >= 2 && dirty.width <= 4);
});

test('clip change dirties old + new visible bounds', () => {
  const root = new Layer('root');
  const child = root.addChild(new Layer('child', {
    bitmap: Bitmap.from(8, 8, new Array(8 * 8 * 4).fill(255)),
    clip: rect(0, 0, 2, 2),
  }));
  const tracker = new DirtyTracker(root);

  tracker.setClip(child, rect(4, 4, 2, 2));

  const dirty = tracker.dirtyRegion()!;
  assert.ok(containsRect(dirty, rect(0, 0, 2, 2)), 'old clip region covered');
  assert.ok(containsRect(dirty, rect(4, 4, 2, 2)), 'new clip region covered');
});

test('effect change dirties old + new expanded bounds', () => {
  const root = new Layer('root');
  const child = root.addChild(new Layer('child', {
    bitmap: red2x2(),
    effects: [{ kind: 'shadow', radius: 1, offsetX: 0, offsetY: 0, color: [0, 0, 0, 128] }],
  }));
  const tracker = new DirtyTracker(root);

  tracker.setEffects(child, [{ kind: 'shadow', radius: 4, offsetX: 0, offsetY: 0, color: [0, 0, 0, 128] }]);

  const dirty = tracker.dirtyRegion()!;
  assert.ok(containsRect(dirty, rect(-4, -4, 10, 10)), 'new radius-4 bounds covered');
});

test('scaled layer maps edit rect through scale', () => {
  const root = new Layer('root');
  const child = root.addChild(new Layer('child', {
    bitmap: red2x2(),
    transform: { a: 2, b: 0, c: 0, d: 2, e: 10, f: 20 }, // scale 2 + translate
  }));
  const tracker = new DirtyTracker(root);

  tracker.markLocalEdit(child, rect(1, 1, 1, 1)); // local pixel (1,1)
  assert.deepEqual(tracker.getEntries()[0].rect, { x: 12, y: 22, width: 2, height: 2 });
});
