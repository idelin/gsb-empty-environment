import { test } from 'node:test';
import assert from 'node:assert/strict';
import { circle, box2, union2, sphere } from '../src/sdf.ts';
import { SdfGrid2D, SdfGrid3D } from '../src/grid.ts';
import { rayMarch2, rayMarch3 } from '../src/raymarch.ts';
import { distance2 } from '../src/vec.ts';

test('ray marching hits a circle at the exact surface point', () => {
  const sdf = circle([0, 0], 0.5);
  const r = rayMarch2(sdf, [-2, 0], [1, 0]);
  assert.ok(r.hit);
  assert.ok(Math.abs(r.distance - 1.5) < 1e-3, `distance ${r.distance}`);
  assert.ok(Math.abs(r.point[0] - -0.5) < 1e-3);
  assert.ok(r.steps <= 8, `converged in ${r.steps} steps (sphere tracing should be fast)`);
  // Normal points back along -x.
  assert.ok(r.normal[0] < -0.99 && Math.abs(r.normal[1]) < 0.01);
});

test('ray marching from an arbitrary origin/direction', () => {
  const sdf = circle([0.2, -0.1], 0.4);
  const origin: [number, number] = [-1.7, 1.3];
  const dir: [number, number] = [0.8, -0.6];
  const r = rayMarch2(sdf, origin, dir);
  assert.ok(r.hit);
  // Hit point lies on the circle.
  assert.ok(Math.abs(distance2(r.point, [0.2, -0.1]) - 0.4) < 1e-3);
  assert.ok(r.steps < 32);
});

test('ray marching misses when pointing away', () => {
  const sdf = circle([0, 0], 0.5);
  const r = rayMarch2(sdf, [-2, 0], [-1, 0], { maxDistance: 10 });
  assert.ok(!r.hit);
});

test('ray marching a composite 2D scene', () => {
  const sdf = union2(circle([-0.4, 0], 0.3), box2([0.4, 0], [0.2, 0.2]));
  const r = rayMarch2(sdf, [0.4, 2], [0, -1]);
  assert.ok(r.hit);
  assert.ok(Math.abs(r.point[1] - 0.2) < 1e-3, `hit y=${r.point[1]}`);
});

test('safe stepping: every step stays outside the geometry', () => {
  const sdf = circle([0, 0], 0.5);
  const origin: [number, number] = [-2, 0.2];
  const dir: [number, number] = [1, -0.1];
  const len = Math.hypot(...dir);
  const d: [number, number] = [dir[0] / len, dir[1] / len];
  let t = 0;
  for (let i = 0; i < 64; i++) {
    const p: [number, number] = [origin[0] + d[0] * t, origin[1] + d[1] * t];
    const v = sdf(p);
    if (Math.abs(v) < 1e-4) return; // reached the surface
    assert.ok(v > 0, `step ${i} penetrated the surface (sdf=${v})`);
    t += Math.abs(v);
  }
  assert.fail('did not converge within 64 steps');
});

test('ray marching against a sampled 64x64 grid', () => {
  const sdf = circle([0, 0], 0.5);
  const grid = SdfGrid2D.fromSdf(sdf, { min: [-1, -1], max: [1, 1] }, 64, 64);
  const field = (p: readonly [number, number]) => grid.sample(p);
  const r = rayMarch2(field, [-2, 0], [1, 0], { stepScale: 0.9 });
  assert.ok(r.hit);
  assert.ok(Math.abs(r.point[0] - -0.5) < 2 * grid.cellX, `hit x=${r.point[0]}`);
  assert.ok(r.steps < 64);
});

test('3D ray marching hits a sphere with correct normal', () => {
  const sdf = sphere([0, 0, 0], 1);
  const r = rayMarch3(sdf, [0, 0, 5], [0, 0, -1]);
  assert.ok(r.hit);
  assert.ok(Math.abs(r.distance - 4) < 1e-3);
  assert.ok(r.steps <= 8, `converged in ${r.steps} steps`);
  assert.ok(r.normal[2] > 0.99, `normal ${r.normal}`);
});

test('3D ray marching against a sampled 64^3 grid', () => {
  const sdf = sphere([0, 0, 0], 0.6);
  const grid = SdfGrid3D.fromSdf(sdf, { min: [-1, -1, -1], max: [1, 1, 1] }, 64, 64, 64);
  const field = (p: readonly [number, number, number]) => grid.sample(p);
  const r = rayMarch3(field, [0.9, 0.9, 0.9], [-1, -1, -1], { stepScale: 0.9 });
  assert.ok(r.hit);
  const radius = Math.hypot(r.point[0], r.point[1], r.point[2]);
  assert.ok(Math.abs(radius - 0.6) < 2 * grid.cellX, `hit radius ${radius}`);
  assert.ok(r.steps < 64);
});
