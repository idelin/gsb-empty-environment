import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  circle, box2, sphere, box3, plane3, torus3,
  union2, subtract2, smoothUnion2, intersect3,
} from '../src/sdf.ts';

test('circle SDF: sign and distance', () => {
  const f = circle([0, 0], 0.5);
  assert.ok(f([0, 0]) < 0, 'centre is inside');
  assert.ok(f([2, 0]) > 0, 'far point is outside');
  assert.ok(Math.abs(f([0.5, 0])) < 1e-12, 'on surface');
  assert.ok(Math.abs(f([0.3, 0.4]) - 0) < 1e-12, '3-4-5 point on surface');
  assert.ok(Math.abs(f([1, 0]) - 0.5) < 1e-12);
});

test('box2 SDF: exact euclidean distance', () => {
  const f = box2([0, 0], [1, 0.5]);
  assert.ok(Math.abs(f([0, 0]) - -0.5) < 1e-12, 'interior distance to nearest face');
  assert.ok(Math.abs(f([2, 0]) - 1) < 1e-12, 'face-normal exterior');
  assert.ok(Math.abs(f([2, 1.5]) - Math.hypot(1, 1)) < 1e-12, 'corner exterior');
});

test('sphere SDF', () => {
  const f = sphere([1, 2, 3], 2);
  assert.ok(Math.abs(f([1, 2, 3]) - -2) < 1e-12);
  assert.ok(Math.abs(f([3, 2, 3])) < 1e-12);
  assert.ok(Math.abs(f([1, 2, 6]) - 1) < 1e-12);
});

test('box3 / plane3 / torus3 sanity', () => {
  const b = box3([0, 0, 0], [1, 1, 1]);
  assert.ok(Math.abs(b([0, 0, 0]) - -1) < 1e-12);
  assert.ok(Math.abs(b([3, 0, 0]) - 2) < 1e-12);
  const pl = plane3([0, 1, 0], 1);
  assert.ok(Math.abs(pl([0, 3, 0]) - 2) < 1e-12);
  const t = torus3([0, 0, 0], 1, 0.25);
  assert.ok(Math.abs(t([1, 0, 0]) - -0.25) < 1e-12, 'tube centre-line is -minor');
  assert.ok(Math.abs(t([1.25, 0, 0])) < 1e-12, 'on the tube surface');
  assert.ok(Math.abs(t([0, 0, 0]) - 0.75) < 1e-12, 'hole centre is outside');
});

test('CSG combinators', () => {
  const a = circle([0, 0], 1);
  const b = circle([1, 0], 1);
  const u = union2(a, b);
  assert.ok(u([1, 0]) < 0, 'union covers second circle');
  const s = subtract2(a, b);
  assert.ok(s([1, 0]) > 0, 'subtraction carves overlap');
  assert.ok(s([-0.9, 0]) < 0, 'subtraction keeps far side');
  const sm = smoothUnion2(0.5, a, b);
  assert.ok(sm([0.5, 0]) < 0, 'smooth union bridges the gap');
  const i3 = intersect3(sphere([0, 0, 0], 1), sphere([1, 0, 0], 1));
  assert.ok(i3([0.5, 0, 0]) < 0);
  assert.ok(i3([1.5, 0, 0]) > 0);
});
