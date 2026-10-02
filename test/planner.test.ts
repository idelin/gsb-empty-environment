import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pack, applyChanges, verifyLayout } from '../src/index.ts';
import type { ItemSpec, MoveOp } from '../src/index.ts';

function opsByType(plan: MoveOp[], type: MoveOp['type']): MoveOp[] {
  return plan.filter((op) => op.type === type);
}

const initialItems: ItemSpec[] = [
  { id: 'hero', w: 8, h: 8, margin: 1 },
  { id: 'icon1', w: 4, h: 4, margin: 1 },
  { id: 'icon2', w: 4, h: 4, margin: 1 },
];

test('adding a texture that fits keeps every existing placement', () => {
  const first = pack(initialItems, { pageW: 16, pageH: 16 });
  const res = applyChanges(first.layout, initialItems, {
    add: [{ id: 'icon3', w: 4, h: 4, margin: 1 }],
  });
  assert.ok(res.ok);
  assert.equal(opsByType(res.plan, 'move').length, 0);
  assert.equal(opsByType(res.plan, 'keep').length, 3);
  assert.equal(opsByType(res.plan, 'place').length, 1);
  assert.equal(res.layout.pages, first.layout.pages);
  const nextItems = [...initialItems, { id: 'icon3', w: 4, h: 4, margin: 1 }];
  assert.ok(verifyLayout(res.layout, nextItems).ok);
});

test('removing a texture emits a remove op and keeps the rest', () => {
  const first = pack(initialItems, { pageW: 16, pageH: 16 });
  const res = applyChanges(first.layout, initialItems, { remove: ['icon2'] });
  assert.ok(res.ok);
  assert.equal(opsByType(res.plan, 'remove').length, 1);
  assert.equal(opsByType(res.plan, 'move').length, 0);
  assert.equal(res.layout.placements.length, 2);
  assert.ok(verifyLayout(res.layout, initialItems.filter((i) => i.id !== 'icon2')).ok);
});

test('resizing in place when the new footprint still fits', () => {
  const first = pack(initialItems, { pageW: 16, pageH: 16 });
  const res = applyChanges(first.layout, initialItems, {
    resize: [{ id: 'icon1', w: 3, h: 3 }],
  });
  assert.ok(res.ok);
  // Shrink: icon1 keeps its spot (size changed but position identical).
  assert.equal(opsByType(res.plan, 'move').length, 0);
  const icon1 = res.layout.placements.find((p) => p.id === 'icon1')!;
  assert.equal(icon1.w, 3);
  assert.equal(icon1.h, 3);
});

test('growing a texture past its space moves only that texture', () => {
  const items: ItemSpec[] = [
    { id: 'a', w: 6, h: 6 },
    { id: 'b', w: 6, h: 6 },
    { id: 'c', w: 4, h: 4 },
  ];
  const first = pack(items, { pageW: 12, pageH: 12 });
  assert.equal(first.layout.pages, 1);
  // Grow c to 8x8: no room left on the page (6+8 > 12), so c must move to a
  // new page while a and b stay put.
  const res = applyChanges(first.layout, items, { resize: [{ id: 'c', w: 8, h: 8 }] });
  assert.ok(res.ok);
  const moves = opsByType(res.plan, 'move');
  assert.equal(moves.length, 1);
  assert.equal(moves[0]!.type === 'move' && moves[0]!.id, 'c');
  assert.equal(opsByType(res.plan, 'keep').length, 2);
  assert.equal(res.layout.pages, 2);
  const nextItems = items.map((i) => (i.id === 'c' ? { ...i, w: 8, h: 8 } : i));
  assert.ok(verifyLayout(res.layout, nextItems).ok);
});

test('fixed textures are never moved by updates', () => {
  const items: ItemSpec[] = [
    { id: 'pin', w: 4, h: 4, fixed: { page: 0, x: 0, y: 0 } },
    { id: 'free', w: 4, h: 4 },
  ];
  const first = pack(items, { pageW: 8, pageH: 8 });
  const res = applyChanges(first.layout, items, { add: [{ id: 'extra', w: 4, h: 4 }] });
  assert.ok(res.ok);
  const pin = res.layout.placements.find((p) => p.id === 'pin')!;
  assert.deepEqual([pin.page, pin.x, pin.y], [0, 0, 0]);
});

test('oversized addition fails atomically: no layout, no plan', () => {
  const first = pack(initialItems, { pageW: 16, pageH: 16 });
  const res = applyChanges(first.layout, initialItems, {
    add: [{ id: 'huge', w: 64, h: 64 }],
  });
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.match(res.error, /huge/);
    assert.equal('layout' in res, false);
    assert.equal('plan' in res, false);
  }
});

test('growing a fixed texture beyond the page fails atomically', () => {
  const items: ItemSpec[] = [{ id: 'pin', w: 4, h: 4, fixed: { page: 0, x: 0, y: 0 } }];
  const first = pack(items, { pageW: 8, pageH: 8 });
  const res = applyChanges(first.layout, items, { resize: [{ id: 'pin', w: 16, h: 16 }] });
  assert.equal(res.ok, false);
});

test('unknown ids in changes are rejected', () => {
  const first = pack(initialItems, { pageW: 16, pageH: 16 });
  assert.equal(applyChanges(first.layout, initialItems, { remove: ['ghost'] }).ok, false);
  assert.equal(
    applyChanges(first.layout, initialItems, { resize: [{ id: 'ghost', w: 1, h: 1 }] }).ok,
    false,
  );
  assert.equal(
    applyChanges(first.layout, initialItems, { add: [{ id: 'hero', w: 2, h: 2 }] }).ok,
    false,
  );
});

test('update result includes bounds with certificate', () => {
  const first = pack(initialItems, { pageW: 16, pageH: 16 });
  const res = applyChanges(first.layout, initialItems, {
    add: [{ id: 'icon3', w: 4, h: 4, margin: 1 }],
  });
  assert.ok(res.ok);
  assert.ok(res.bounds.lower >= 1);
  assert.ok(res.bounds.upper >= res.bounds.lower);
  assert.equal(res.bounds.certificate, 'exact'); // 4 movable items <= threshold
});

test('updates are deterministic', () => {
  const first = pack(initialItems, { pageW: 16, pageH: 16 });
  const change = {
    add: [{ id: 'n1', w: 2, h: 6 }, { id: 'n2', w: 5, h: 5, margin: 1 }],
    resize: [{ id: 'icon2', w: 5, h: 3 }],
  };
  const r1 = applyChanges(first.layout, initialItems, change);
  const r2 = applyChanges(first.layout, initialItems, change);
  assert.deepEqual(r1, r2);
});
