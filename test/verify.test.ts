import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pack, verifyLayout } from '../src/index.ts';
import type { ItemSpec, Layout } from '../src/index.ts';

const baseItems: ItemSpec[] = [
  { id: 'a', w: 4, h: 4 },
  { id: 'b', w: 4, h: 4 },
];

function validLayout(): Layout {
  return pack(baseItems, { pageW: 8, pageH: 8 }).layout;
}

test('pixel verifier accepts packer output', () => {
  const layout = validLayout();
  const report = verifyLayout(layout, baseItems);
  assert.ok(report.ok, report.errors.join('; '));
});

test('pixel verifier catches overlapping placements', () => {
  const layout = validLayout();
  // Force b exactly on top of a.
  layout.placements[1] = { ...layout.placements[0]!, id: 'b' };
  const report = verifyLayout(layout, baseItems);
  assert.equal(report.ok, false);
  assert.ok(report.errors.some((e) => e.includes('overlap')));
});

test('pixel verifier catches margin-zone collisions', () => {
  const items: ItemSpec[] = [
    { id: 'a', w: 4, h: 4, margin: 2 },
    { id: 'b', w: 4, h: 4 },
  ];
  const layout: Layout = {
    pageW: 16,
    pageH: 16,
    pages: 1,
    placements: [
      // a's footprint is (0,0)-(8,8): texture at (2,2) with 2px ring.
      { id: 'a', page: 0, x: 2, y: 2, w: 4, h: 4, rotated: false, margin: { top: 2, right: 2, bottom: 2, left: 2 } },
      // b at (6,0) overlaps a's right margin ring (x 6..8).
      { id: 'b', page: 0, x: 6, y: 0, w: 4, h: 4, rotated: false, margin: { top: 0, right: 0, bottom: 0, left: 0 } },
    ],
    unpacked: [],
  };
  const report = verifyLayout(layout, items);
  assert.equal(report.ok, false);
  assert.ok(report.errors.some((e) => e.includes('overlap')));
});

test('pixel verifier catches out-of-bounds placements', () => {
  const layout = validLayout();
  layout.placements[0] = { ...layout.placements[0]!, x: 6, y: 6 }; // 6+4 > 8
  const report = verifyLayout(layout, baseItems);
  assert.equal(report.ok, false);
  assert.ok(report.errors.some((e) => e.includes('out of page')));
});

test('pixel verifier catches moved fixed items', () => {
  const items: ItemSpec[] = [{ id: 'pin', w: 2, h: 2, fixed: { page: 0, x: 1, y: 1 } }];
  const layout = pack(items, { pageW: 8, pageH: 8 }).layout;
  layout.placements[0] = { ...layout.placements[0]!, x: 3 };
  const report = verifyLayout(layout, items);
  assert.equal(report.ok, false);
  assert.ok(report.errors.some((e) => e.includes('moved')));
});

test('pixel verifier catches illegal rotation and size mismatches', () => {
  const items: ItemSpec[] = [{ id: 'a', w: 4, h: 2, rotatable: false }];
  const layout: Layout = {
    pageW: 8,
    pageH: 8,
    pages: 1,
    placements: [
      { id: 'a', page: 0, x: 0, y: 0, w: 2, h: 4, rotated: true, margin: { top: 0, right: 0, bottom: 0, left: 0 } },
    ],
    unpacked: [],
  };
  const report = verifyLayout(layout, items);
  assert.equal(report.ok, false);
  assert.ok(report.errors.some((e) => e.includes('forbids rotation')));
});

test('pixel verifier catches missing items', () => {
  const layout = validLayout();
  layout.placements.pop();
  const report = verifyLayout(layout, baseItems);
  assert.equal(report.ok, false);
  assert.ok(report.errors.some((e) => e.includes('neither placed')));
});
