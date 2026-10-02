import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pack, verifyAll, fitsPage } from '../src/index.ts';
import type { ItemSpec } from '../src/index.ts';

const base = { pageWidth: 32, pageHeight: 32, spacing: 2 };

test('packs every item and the independent verifier agrees', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 10, height: 6 },
    { id: 'b', width: 6, height: 10 },
    { id: 'c', width: 4, height: 4, margin: 1 },
    { id: 'd', width: 8, height: 3 },
  ];
  const r = pack({ ...base, items });
  assert.equal(r.unpacked.length, 0);
  assert.equal(r.placements.length, 4);
  const v = verifyAll(base.pageWidth, base.pageHeight, base.spacing, items, r.placements);
  assert.deepEqual(v.errors, []);
  assert.ok(v.ok);
});

test('deterministic: identical inputs give byte-identical layouts', () => {
  const items: ItemSpec[] = Array.from({ length: 12 }, (_, i) => ({
    id: `it${i}`,
    width: 3 + ((i * 7) % 9),
    height: 2 + ((i * 5) % 7),
    margin: i % 3,
  }));
  const r1 = pack({ ...base, items });
  const r2 = pack({ ...base, items: [...items].reverse() });
  assert.deepEqual(r1.placements, r2.placements);
});

test('spacing is charged into capacity', () => {
  // Page 10x10, item 6x6: two items never fit side by side with spacing 4
  // (6+4+6 > 10), so they must land on different pages.
  const items: ItemSpec[] = [
    { id: 'a', width: 6, height: 6 },
    { id: 'b', width: 6, height: 6 },
  ];
  const r = pack({ pageWidth: 10, pageHeight: 10, spacing: 4, items });
  assert.equal(r.pages, 2);
  const v = verifyAll(10, 10, 4, items, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('rotation is used when it helps and UV-safe size swap holds', () => {
  const items: ItemSpec[] = [{ id: 'wide', width: 28, height: 4 }];
  const r = pack({ pageWidth: 10, pageHeight: 32, spacing: 0, items });
  assert.equal(r.unpacked.length, 0);
  const p = r.placements[0]!;
  assert.equal(p.rotated, true);
  assert.equal(p.width, 4);
  assert.equal(p.height, 28);
});

test('rotatable:false forbids rotation and can make an item unpackable', () => {
  const items: ItemSpec[] = [{ id: 'wide', width: 28, height: 4, rotatable: false }];
  const r = pack({ pageWidth: 10, pageHeight: 32, spacing: 0, items });
  assert.deepEqual(r.unpacked, [{ id: 'wide', reason: 'too-large' }]);
  assert.equal(r.placements.length, 0);
});

test('oversized items fail explicitly instead of overflowing the page', () => {
  const items: ItemSpec[] = [
    { id: 'huge', width: 100, height: 100 },
    { id: 'ok', width: 4, height: 4 },
  ];
  const r = pack({ ...base, items });
  assert.deepEqual(r.unpacked, [{ id: 'huge', reason: 'too-large' }]);
  const p = r.placements.find((p) => p.id === 'ok')!;
  assert.ok(p.x >= 0 && p.y >= 0 && p.x + p.width <= 32 && p.y + p.height <= 32);
  assert.equal(fitsPage({ ...base, items }, items[0]!), false);
});

test('fixed items stay pinned and others flow around them', () => {
  const items: ItemSpec[] = [
    { id: 'pin', width: 8, height: 8, fixed: { page: 0, x: 12, y: 12 } },
    { id: 'f1', width: 6, height: 6 },
    { id: 'f2', width: 6, height: 6 },
    { id: 'f3', width: 6, height: 6 },
  ];
  const r = pack({ ...base, items });
  const pin = r.placements.find((p) => p.id === 'pin')!;
  assert.deepEqual([pin.page, pin.x, pin.y], [0, 12, 12]);
  const v = verifyAll(base.pageWidth, base.pageHeight, base.spacing, items, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('conflicting fixed anchors are reported, never overlapped', () => {
  const items: ItemSpec[] = [
    { id: 'p1', width: 8, height: 8, fixed: { page: 0, x: 0, y: 0 } },
    { id: 'p2', width: 8, height: 8, fixed: { page: 0, x: 4, y: 4 } },
  ];
  const r = pack({ ...base, items });
  assert.equal(r.placements.length, 1);
  assert.deepEqual(r.unpacked, [{ id: 'p2', reason: 'fixed-conflict' }]);
});

test('per-item margins enlarge the keep-out zone', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 4, height: 4, margin: 3 },
    { id: 'b', width: 4, height: 4 },
  ];
  const r = pack({ pageWidth: 32, pageHeight: 12, spacing: 0, items });
  const a = r.placements.find((p) => p.id === 'a')!;
  const b = r.placements.find((p) => p.id === 'b')!;
  const gap = Math.max(
    0,
    Math.max(a.x - (b.x + b.width), b.x - (a.x + a.width)),
    Math.max(a.y - (b.y + b.height), b.y - (a.y + a.height)),
  );
  assert.ok(gap >= 3, `expected margin gap >= 3, got ${gap}`);
});

test('many items spill onto multiple pages, all verified', () => {
  const items: ItemSpec[] = Array.from({ length: 40 }, (_, i) => ({
    id: `s${i}`,
    width: 5 + (i % 4),
    height: 5 + ((i * 3) % 4),
  }));
  const r = pack({ pageWidth: 24, pageHeight: 24, spacing: 1, items });
  assert.equal(r.unpacked.length, 0);
  assert.ok(r.pages > 1);
  const v = verifyAll(24, 24, 1, items, r.placements);
  assert.ok(v.ok, v.errors.join(';'));
});

test('first-fit reuses open pages instead of one page per item', () => {
  // 30 items (> exact threshold) of 8x8 in a 16x16 page: 4 per page -> <= 8 pages.
  const items: ItemSpec[] = Array.from({ length: 30 }, (_, i) => ({
    id: `r${i}`, width: 8, height: 8,
  }));
  const r = pack({ pageWidth: 16, pageHeight: 16, spacing: 0, items });
  assert.equal(r.unpacked.length, 0);
  assert.ok(r.pages <= 8, `expected <= 8 pages, got ${r.pages}`);
});

test('duplicate ids and bad dimensions are rejected', () => {
  assert.throws(() =>
    pack({ ...base, items: [
      { id: 'x', width: 2, height: 2 },
      { id: 'x', width: 3, height: 3 },
    ] }),
  );
  assert.throws(() => pack({ ...base, items: [{ id: 'x', width: 0, height: 2 }] }));
});
