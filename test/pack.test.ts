import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pack, verifyLayout, exactFeasible, validateItems } from '../src/index.ts';
import type { ItemSpec } from '../src/index.ts';

test('packs basic rectangles and reports coordinates', () => {
  const items: ItemSpec[] = [
    { id: 'a', w: 4, h: 4 },
    { id: 'b', w: 4, h: 4 },
    { id: 'c', w: 8, h: 2 },
  ];
  const { layout, bounds } = pack(items, { pageW: 8, pageH: 8 });
  assert.equal(layout.pages, 1);
  assert.equal(layout.placements.length, 3);
  assert.equal(layout.unpacked.length, 0);
  for (const p of layout.placements) {
    assert.ok(p.x >= 0 && p.y >= 0);
    assert.ok(p.x + p.w <= 8 && p.y + p.h <= 8);
  }
  assert.equal(bounds.certificate, 'exact');
  assert.equal(bounds.optimal, 1);
  assert.ok(verifyLayout(layout, items).ok);
});

test('is deterministic across runs and input shuffles', () => {
  const items: ItemSpec[] = [
    { id: 'a', w: 3, h: 5, margin: 1 },
    { id: 'b', w: 6, h: 2 },
    { id: 'c', w: 2, h: 2, rotatable: false },
    { id: 'd', w: 4, h: 4, margin: { top: 1, right: 2 } },
    { id: 'e', w: 7, h: 1 },
  ];
  const r1 = pack(items, { pageW: 12, pageH: 12 });
  const r2 = pack(items, { pageW: 12, pageH: 12 });
  assert.deepEqual(r1.layout, r2.layout);
  const shuffled = pack([...items].reverse(), { pageW: 12, pageH: 12 });
  assert.deepEqual(r1.layout, shuffled.layout);
});

test('spacing edges count toward capacity', () => {
  // Two 5x5 textures with 1px margin have 7x7 footprints: they cannot share
  // a 10x10 page (7+7 > 10), although the bare textures (5+5 = 10) would fit.
  const items: ItemSpec[] = [
    { id: 'a', w: 5, h: 5, margin: 1 },
    { id: 'b', w: 5, h: 5, margin: 1 },
  ];
  const { layout, bounds } = pack(items, { pageW: 10, pageH: 10 });
  assert.equal(layout.pages, 2);
  assert.equal(bounds.optimal, 2);
  assert.ok(verifyLayout(layout, items).ok);

  // Without margins they share one page.
  const bare = pack(items.map((i) => ({ ...i, margin: 0 })), { pageW: 10, pageH: 10 });
  assert.equal(bare.layout.pages, 1);
});

test('per-side margins are honored', () => {
  const items: ItemSpec[] = [{ id: 'a', w: 4, h: 4, margin: { left: 2, top: 1, right: 0, bottom: 0 } }];
  const { layout } = pack(items, { pageW: 8, pageH: 8 });
  const p = layout.placements[0]!;
  assert.equal(p.x, 2); // footprint starts at 0, texture offset by left margin
  assert.equal(p.y, 1);
  assert.equal(p.margin.left, 2);
  assert.equal(p.margin.top, 1);
});

test('rotates 90 degrees when it helps and rotation is allowed', () => {
  // 5x3 does not fit a 4-wide page unrotated, but fits as 3x5 rotated.
  const items: ItemSpec[] = [{ id: 'r', w: 5, h: 3 }];
  const { layout } = pack(items, { pageW: 4, pageH: 6 });
  assert.equal(layout.unpacked.length, 0);
  const p = layout.placements[0]!;
  assert.equal(p.rotated, true);
  assert.equal(p.w, 3);
  assert.equal(p.h, 5);
});

test('rotation restriction is respected and oversized fails explicitly', () => {
  const items: ItemSpec[] = [
    { id: 'norot', w: 5, h: 3, rotatable: false },
    { id: 'huge', w: 100, h: 100 },
    { id: 'ok', w: 2, h: 2 },
  ];
  const { layout } = pack(items, { pageW: 4, pageH: 6 });
  const reasons = new Map(layout.unpacked.map((u) => [u.id, u.reason]));
  assert.equal(reasons.get('norot'), 'too-large');
  assert.equal(reasons.get('huge'), 'too-large');
  assert.equal(layout.placements.length, 1);
  assert.equal(layout.placements[0]!.id, 'ok');
  // The layout itself stays valid: failure is explicit, never an overflow.
  assert.ok(verifyLayout(layout, items).ok);
});

test('fixed items never move and others pack around them', () => {
  const items: ItemSpec[] = [
    { id: 'pin', w: 4, h: 4, fixed: { page: 0, x: 2, y: 2 } },
    { id: 'a', w: 2, h: 8 },
    { id: 'b', w: 8, h: 1 },
  ];
  const { layout } = pack(items, { pageW: 8, pageH: 8 });
  const pin = layout.placements.find((p) => p.id === 'pin')!;
  assert.deepEqual([pin.page, pin.x, pin.y, pin.rotated], [0, 2, 2, false]);
  assert.ok(verifyLayout(layout, items).ok);
});

test('fixed item outside the page is rejected, not published', () => {
  const items: ItemSpec[] = [{ id: 'pin', w: 4, h: 4, fixed: { page: 0, x: 6, y: 6 } }];
  const { layout } = pack(items, { pageW: 8, pageH: 8 });
  assert.equal(layout.placements.length, 0);
  assert.equal(layout.unpacked[0]!.id, 'pin');
  assert.equal(layout.unpacked[0]!.reason, 'invalid');
});

test('exact solver proves optimality on small instances', () => {
  // Two 8x5 textures cannot share an 8x8 page in any orientation.
  const items: ItemSpec[] = [
    { id: 'a', w: 8, h: 5 },
    { id: 'b', w: 8, h: 5 },
  ];
  const { layout, bounds } = pack(items, { pageW: 8, pageH: 8 });
  assert.equal(bounds.certificate, 'exact');
  assert.equal(bounds.optimal, 2);
  assert.equal(bounds.lower, 2);
  assert.equal(layout.pages, 2);
});

test('exact solver finds a better page count than the greedy order suggests', () => {
  // 6x6 + three 4x4 in a 10x10 page: fits exactly (36 + 3*16 = 84 <= 100 and
  // geometrically: 6x6 corner, 4x4 beside, two 4x4 below).
  const items: ItemSpec[] = [
    { id: 'big', w: 6, h: 6 },
    { id: 's1', w: 4, h: 4 },
    { id: 's2', w: 4, h: 4 },
    { id: 's3', w: 4, h: 4 },
  ];
  const { layout, bounds } = pack(items, { pageW: 10, pageH: 10 });
  assert.equal(bounds.optimal, 1);
  assert.equal(layout.pages, 1);
  assert.ok(verifyLayout(layout, items).ok);
});

test('large instances return bounds with a heuristic label', () => {
  const items: ItemSpec[] = [];
  for (let i = 0; i < 40; i++) {
    items.push({ id: `t${i}`, w: 3 + (i % 5), h: 2 + (i % 7), margin: 1 });
  }
  const { layout, bounds } = pack(items, { pageW: 32, pageH: 32, exactThreshold: 12 });
  assert.equal(bounds.certificate, 'heuristic');
  assert.equal(bounds.optimal, null);
  assert.ok(bounds.lower >= 1);
  assert.ok(bounds.upper >= bounds.lower);
  assert.equal(bounds.upper, layout.pages);
  assert.ok(verifyLayout(layout, items).ok);
});

test('exactFeasible agrees with brute reasoning on a tiny case', () => {
  const { valid } = validateItems([
    { id: 'a', w: 4, h: 4 },
    { id: 'b', w: 4, h: 4 },
    { id: 'c', w: 4, h: 4 },
    { id: 'd', w: 4, h: 4 },
    { id: 'e', w: 4, h: 4 },
  ]);
  const one = exactFeasible(valid, 8, 8, 1, []);
  assert.equal(one.feasible, false); // 5 x 16px = 80 > 64
  const two = exactFeasible(valid, 8, 8, 2, []);
  assert.equal(two.feasible, true);
});

test('invalid input is rejected with reasons', () => {
  const { layout } = pack(
    [
      { id: 'neg', w: -1, h: 4 },
      { id: 'frac', w: 2.5, h: 2 },
      { id: 'badmargin', w: 2, h: 2, margin: -1 },
    ],
    { pageW: 8, pageH: 8 },
  );
  assert.equal(layout.placements.length, 0);
  assert.deepEqual(
    layout.unpacked.map((u) => u.reason),
    ['invalid', 'invalid', 'invalid'],
  );
});
