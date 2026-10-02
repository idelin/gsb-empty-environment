import { test } from "node:test";
import assert from "node:assert/strict";
import { AABB } from "../src/aabb.ts";

test("surfaceArea: 3D box", () => {
  const box = new AABB([0, 0, 0], [2, 3, 4]);
  assert.equal(box.surfaceArea(), 2 * (2 * 3 + 3 * 4 + 4 * 2));
});

test("surfaceArea: 2D box degenerates to perimeter", () => {
  const box = new AABB([0, 0], [3, 5]);
  assert.equal(box.surfaceArea(), 2 * (3 + 5));
});

test("union contains both operands", () => {
  const a = new AABB([0, 0, 0], [1, 1, 1]);
  const b = new AABB([2, -1, 0.5], [3, 4, 0.75]);
  const u = AABB.union(a, b);
  assert.ok(u.contains(a));
  assert.ok(u.contains(b));
  assert.deepEqual(u.min, [0, -1, 0]);
  assert.deepEqual(u.max, [3, 4, 1]);
});

test("overlaps detects intersection and separation", () => {
  const a = new AABB([0, 0], [2, 2]);
  assert.ok(a.overlaps(new AABB([1, 1], [3, 3])));
  assert.ok(!a.overlaps(new AABB([2.5, 0], [3, 1])));
});

test("rayHit: slab test entry distance and miss", () => {
  const box = new AABB([1, 1, 1], [2, 2, 2]);
  const inv = [1, 1 / 0, 1 / 0].map((v) => v); // +x direction
  const t = box.rayHit([0, 1.5, 1.5], inv, Infinity);
  assert.equal(t, 1);
  assert.equal(box.rayHit([0, 5, 5], inv, Infinity), null);
  // maxT nearer than the box rejects the hit
  assert.equal(box.rayHit([0, 1.5, 1.5], inv, 0.5), null);
});

test("rayHit: origin inside box returns t=0", () => {
  const box = new AABB([0, 0, 0], [1, 1, 1]);
  const t = box.rayHit([0.5, 0.5, 0.5], [1, 0, 0].map((d) => 1 / d), Infinity);
  assert.equal(t, 0);
});
