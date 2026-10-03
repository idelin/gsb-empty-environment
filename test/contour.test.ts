import { test } from 'node:test';
import assert from 'node:assert/strict';
import { circle, box2, union2 } from '../src/sdf.ts';
import { SdfGrid2D } from '../src/grid.ts';
import { extractContour2D } from '../src/contour2d.ts';
import { distance2 } from '../src/vec.ts';

test('marching squares extracts a closed circle contour', () => {
  const sdf = circle([0, 0], 0.5);
  const grid = SdfGrid2D.fromSdf(sdf, { min: [-1, -1], max: [1, 1] }, 64, 64);
  const segments = extractContour2D(grid);

  assert.ok(segments.length > 40, `expected a detailed contour, got ${segments.length}`);

  // Every vertex lies on the circle within interpolation tolerance.
  const tol = 2 * Math.max(grid.cellX, grid.cellY);
  for (const [a, b] of segments) {
    for (const p of [a, b]) {
      assert.ok(Math.abs(distance2(p, [0, 0]) - 0.5) < tol, `vertex ${p} off surface`);
    }
  }

  // Contour is closed: endpoints pair up (each endpoint shared by 2 segments).
  const count = new Map<string, number>();
  for (const [a, b] of segments) {
    for (const p of [a, b]) {
      const key = `${p[0].toFixed(6)},${p[1].toFixed(6)}`;
      count.set(key, (count.get(key) ?? 0) + 1);
    }
  }
  for (const [key, n] of count) {
    assert.equal(n, 2, `vertex ${key} should be shared by exactly 2 segments`);
  }
});

test('marching squares on a composite (union) shape', () => {
  const sdf = union2(circle([-0.3, 0], 0.3), box2([0.3, 0], [0.25, 0.25]));
  const grid = SdfGrid2D.fromSdf(sdf, { min: [-1, -1], max: [1, 1] }, 64, 64);
  const segments = extractContour2D(grid);
  assert.ok(segments.length > 60);
  // Sampled field at every contour vertex must be ~iso.
  for (const [a, b] of segments) {
    for (const p of [a, b]) {
      assert.ok(Math.abs(grid.sample(p)) < 1e-6);
    }
  }
});

test('non-zero iso level offsets the contour', () => {
  const sdf = circle([0, 0], 0.5);
  const grid = SdfGrid2D.fromSdf(sdf, { min: [-1, -1], max: [1, 1] }, 64, 64);
  const grown = extractContour2D(grid, 0.1);
  for (const [a] of grown) {
    assert.ok(Math.abs(distance2(a, [0, 0]) - 0.6) < 3 * grid.cellX);
  }
});
