import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RGBAImage, colorForId, verifyAll, verifyLayout } from '../src/index.ts';
import type { ItemSpec, Placement } from '../src/index.ts';

test('RGBAImage fills, counts and detects claimed pixels', () => {
  const img = new RGBAImage(8, 8);
  assert.equal(img.countClaimed(), 0);
  const clashes = img.fillRect(2, 2, 3, 3, [255, 0, 0, 255]);
  assert.equal(clashes, 0);
  assert.equal(img.countClaimed(), 9);
  const again = img.fillRect(3, 3, 2, 2, [0, 255, 0, 255]);
  assert.equal(again, 4);
  assert.deepEqual(img.getPixel(3, 3), [0, 255, 0, 255]);
  assert.throws(() => img.fillRect(6, 6, 3, 3, [0, 0, 0, 255]));
  assert.throws(() => img.getPixel(8, 0));
});

test('colorForId is deterministic and opaque', () => {
  assert.deepEqual(colorForId('hero'), colorForId('hero'));
  assert.equal(colorForId('hero')[3], 255);
});

test('verifier flags overlapping placements the packer never made', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 6, height: 6 },
    { id: 'b', width: 6, height: 6 },
  ];
  const bad: Placement[] = [
    { id: 'a', page: 0, x: 0, y: 0, width: 6, height: 6, rotated: false },
    { id: 'b', page: 0, x: 4, y: 4, width: 6, height: 6, rotated: false },
  ];
  const v = verifyAll(32, 32, 0, items, bad);
  assert.equal(v.ok, false);
  assert.ok(v.errors.some((e) => e.includes('overlap') || e.includes('gap')));
});

test('verifier flags spacing violations even without touching sprites', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 4, height: 4 },
    { id: 'b', width: 4, height: 4 },
  ];
  const placements: Placement[] = [
    { id: 'a', page: 0, x: 0, y: 0, width: 4, height: 4, rotated: false },
    { id: 'b', page: 0, x: 5, y: 0, width: 4, height: 4, rotated: false }, // gap 1 < spacing 4
  ];
  const v = verifyLayout(32, 32, 4, items, placements);
  assert.equal(v.ok, false);
});

test('verifier flags out-of-bounds, bad rotation and moved fixed items', () => {
  const items: ItemSpec[] = [
    { id: 'a', width: 4, height: 4, rotatable: false },
    { id: 'b', width: 4, height: 4, fixed: { page: 0, x: 8, y: 8 } },
  ];
  const placements: Placement[] = [
    { id: 'a', page: 0, x: 30, y: 0, width: 4, height: 4, rotated: true }, // oob + illegal rotation
    { id: 'b', page: 0, x: 9, y: 8, width: 4, height: 4, rotated: false }, // moved
  ];
  const v = verifyLayout(32, 32, 0, items, placements);
  assert.equal(v.ok, false);
  assert.ok(v.errors.some((e) => e.includes('out of page bounds')));
  assert.ok(v.errors.some((e) => e.includes('rotation not allowed')));
  assert.ok(v.errors.some((e) => e.includes('fixed item moved')));
});

test('verifier accepts margin-clipped footprints at page edges', () => {
  const items: ItemSpec[] = [{ id: 'a', width: 4, height: 4, margin: 2 }];
  const placements: Placement[] = [
    { id: 'a', page: 0, x: 0, y: 0, width: 4, height: 4, rotated: false },
  ];
  const v = verifyLayout(32, 32, 2, items, placements);
  assert.ok(v.ok, v.errors.join(';'));
});
