import { test } from 'node:test';
import assert from 'node:assert/strict';
import { circle, sphere } from '../src/sdf.ts';
import { SdfGrid2D, SdfGrid3D } from '../src/grid.ts';
import { length2 } from '../src/vec.ts';

test('64x64 grid rasterization of a circle SDF', () => {
  const sdf = circle([0, 0], 0.5);
  const grid = SdfGrid2D.fromSdf(sdf, { min: [-1, -1], max: [1, 1] }, 64, 64);

  assert.equal(grid.nx, 64);
  assert.equal(grid.ny, 64);
  assert.equal(grid.data.length, 64 * 64);

  // Lattice points land exactly on the analytic field.
  const i = 32;
  const x = -1 + i * grid.cellX;
  const y = -1 + 32 * grid.cellY;
  assert.ok(Math.abs(grid.at(i, 32) - sdf([x, y])) < 1e-6);

  // Sign structure: centre negative, corners positive.
  assert.ok(grid.at(32, 32) < 0);
  assert.ok(grid.at(0, 0) > 0);
  assert.ok(grid.at(63, 63) > 0);
});

test('bilinear sampling approximates the analytic field', () => {
  const sdf = circle([0.1, -0.2], 0.4);
  const grid = SdfGrid2D.fromSdf(sdf, { min: [-1, -1], max: [1, 1] }, 64, 64);
  let maxErr = 0;
  for (let n = 0; n < 500; n++) {
    const p: [number, number] = [
      -0.95 + (n * 0.731) % 1.9,
      -0.95 + (n * 0.377) % 1.9,
    ];
    maxErr = Math.max(maxErr, Math.abs(grid.sample(p) - sdf(p)));
  }
  // Bilinear interpolation of a smooth field on a 64x64 grid over [-1,1]^2.
  assert.ok(maxErr < 5e-3, `max interpolation error ${maxErr}`);
});

test('grid gradient approximates analytic gradient', () => {
  const sdf = circle([0, 0], 0.5);
  const grid = SdfGrid2D.fromSdf(sdf, { min: [-1, -1], max: [1, 1] }, 64, 64);
  const p: [number, number] = [0.8, 0.3];
  const g = grid.gradient(p);
  const len = length2(g);
  const analytic: [number, number] = [p[0] / length2(p), p[1] / length2(p)];
  assert.ok(Math.abs(g[0] / len - analytic[0]) < 1e-2);
  assert.ok(Math.abs(g[1] / len - analytic[1]) < 1e-2);
});

test('64^3 grid rasterization of a sphere SDF', () => {
  const sdf = sphere([0, 0, 0], 0.6);
  const grid = SdfGrid3D.fromSdf(sdf, { min: [-1, -1, -1], max: [1, 1, 1] }, 64, 64, 64);
  assert.equal(grid.data.length, 64 ** 3);
  assert.ok(grid.at(32, 32, 32) < 0);
  assert.ok(grid.at(0, 0, 0) > 0);
  // Trilinear sample near the surface is close to the analytic value.
  const p: [number, number, number] = [0.61, 0.05, -0.03];
  assert.ok(Math.abs(grid.sample(p) - sdf(p)) < 5e-3);
});
