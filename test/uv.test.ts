import { test } from 'node:test';
import assert from 'node:assert/strict';
import { uvToPage, pageToUv, uvCorners } from '../src/index.ts';

test('unrotated uv maps linearly into the page', () => {
  const p = { id: 'a', page: 0, x: 10, y: 20, width: 8, height: 4, rotated: false };
  assert.deepEqual(uvToPage(p, 0, 0), { x: 10, y: 20 });
  assert.deepEqual(uvToPage(p, 1, 1), { x: 18, y: 24 });
  assert.deepEqual(uvToPage(p, 0.5, 0.5), { x: 14, y: 22 });
});

test('rotated uv follows a 90-degree clockwise mapping', () => {
  // Texture 8x4 placed rotated: box is 4 wide, 8 tall.
  const p = { id: 'a', page: 0, x: 10, y: 20, width: 4, height: 8, rotated: true };
  // Original top-left texel lands on the top-right corner of the box.
  assert.deepEqual(uvToPage(p, 0, 0), { x: 14, y: 20 });
  // Original top-right lands on the bottom-right.
  assert.deepEqual(uvToPage(p, 1, 0), { x: 14, y: 28 });
  // Original bottom-left lands on the top-left.
  assert.deepEqual(uvToPage(p, 0, 1), { x: 10, y: 20 });
  // Original bottom-right lands on the bottom-left.
  assert.deepEqual(uvToPage(p, 1, 1), { x: 10, y: 28 });
});

test('pageToUv inverts uvToPage on a grid of samples', () => {
  const p = { id: 'a', page: 0, x: 3, y: 7, width: 4, height: 8, rotated: true };
  for (const u of [0, 0.25, 0.5, 0.75, 1]) {
    for (const v of [0, 0.25, 0.5, 0.75, 1]) {
      const xy = uvToPage(p, u, v);
      const uv = pageToUv(p, xy.x, xy.y);
      assert.ok(Math.abs(uv.u - u) < 1e-9 && Math.abs(uv.v - v) < 1e-9);
    }
  }
});

test('uv corners stay inside the placed box', () => {
  const p = { id: 'a', page: 0, x: 5, y: 5, width: 4, height: 8, rotated: true };
  for (const c of uvCorners(p)) {
    assert.ok(c.x >= p.x && c.x <= p.x + p.width);
    assert.ok(c.y >= p.y && c.y <= p.y + p.height);
  }
});

test('uv out of range throws', () => {
  const p = { id: 'a', page: 0, x: 0, y: 0, width: 4, height: 4, rotated: false };
  assert.throws(() => uvToPage(p, -0.1, 0.5));
  assert.throws(() => uvToPage(p, 0.5, 1.1));
});
