import test from "node:test";
import assert from "node:assert/strict";
import {
  Mask,
  rasterizeShape,
  union,
  intersect,
  subtract,
  invert,
  clamp01,
} from "../src/mask.ts";
import { rectShape, pathShape } from "../src/geometry.ts";
import { translate } from "../src/matrix.ts";

test("integer-aligned rect rasterizes to exact 0/1 coverage", () => {
  const m = rasterizeShape(rectShape(2, 1, 3, 2), 8, 6, undefined, 4);
  assert.equal(m.at(2, 1), 1);
  assert.equal(m.at(4, 2), 1);
  assert.equal(m.at(1, 1), 0);
  assert.equal(m.at(5, 1), 0); // half-open edge: x+w excluded
  assert.equal(m.at(2, 3), 0);
});

test("shifted rect produces soft edges within [0,1]", () => {
  const m = rasterizeShape(rectShape(1.5, 1.5, 2, 2), 5, 5, undefined, 4);
  assert.equal(m.at(1, 1), 0.25); // quarter of the 4x4 subpixels covered
  assert.equal(m.at(2, 2), 1);
  assert.equal(m.at(1, 2), 0.5);
  for (const v of m.data) assert.ok(v >= 0 && v <= 1, `coverage ${v} out of range`);
});

test("boolean ops follow the fractional-coverage definitions", () => {
  const a = Mask.filled(4, 3, 0.6);
  const b = Mask.filled(4, 3, 0.5);
  for (const v of union(a, b).data) assert.ok(Math.abs(v - 0.8) < 1e-6);
  for (const v of intersect(a, b).data) assert.ok(Math.abs(v - 0.3) < 1e-6);
  for (const v of subtract(a, b).data) assert.ok(Math.abs(v - 0.3) < 1e-6);
});

test("coverage never leaves [0,1] under random boolean combinations", () => {
  let seed = 12345;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let trial = 0; trial < 50; trial++) {
    const a = Mask.filled(3, 3, rand());
    const b = Mask.filled(3, 3, rand());
    for (const m of [union(a, b), intersect(a, b), subtract(a, b), subtract(b, a)]) {
      for (const v of m.data) assert.ok(v >= 0 && v <= 1, `coverage ${v} out of range`);
    }
  }
});

test("De Morgan: ~(a|b) === ~a & ~b", () => {
  const a = rasterizeShape(rectShape(0.5, 0.5, 3, 3), 6, 6, undefined, 4);
  const b = rasterizeShape(rectShape(2.5, 2.5, 3, 3), 6, 6, undefined, 4);
  const lhs = invert(union(a, b));
  const rhs = intersect(invert(a), invert(b));
  for (let i = 0; i < lhs.data.length; i++) {
    assert.ok(Math.abs(lhs.data[i] - rhs.data[i]) < 1e-6);
  }
});

test("inverse mask complements only inside the given region", () => {
  const m = Mask.filled(6, 4, 0.25);
  const inv = invert(m, { x: 1, y: 1, w: 3, h: 2 });
  assert.equal(inv.at(1, 1), 0.75);
  assert.equal(inv.at(3, 2), 0.75);
  assert.equal(inv.at(0, 0), 0); // outside region stays 0, not 0.75
  assert.equal(inv.at(4, 1), 0);
  assert.equal(inv.at(1, 3), 0);
});

test("inverse mask over the whole canvas never leaks outside", () => {
  const empty = new Mask(5, 5);
  const inv = invert(empty);
  for (const v of inv.data) assert.equal(v, 1);
  assert.equal(inv.data.length, 25); // complement is bounded by the canvas
});

test("triangle path: total coverage approximates exact area", () => {
  const tri = pathShape([[0, 0, 4, 0, 0, 4]]);
  const m = rasterizeShape(tri, 4, 4, undefined, 16);
  let sum = 0;
  for (const v of m.data) sum += v;
  assert.ok(Math.abs(sum - 8) < 0.3, `coverage sum ${sum} should be ≈ 8 (area)`);
});

test("evenodd path: overlapping subpaths cancel out", () => {
  const p = pathShape(
    [
      [0, 0, 2, 0, 2, 2, 0, 2],
      [1, 1, 3, 1, 3, 3, 1, 3],
    ],
    "evenodd",
  );
  const m = rasterizeShape(p, 4, 4, undefined, 4);
  assert.equal(m.at(0, 0), 1); // only in first square
  assert.equal(m.at(1, 1), 0); // in both -> cancelled
  assert.equal(m.at(2, 2), 1); // only in second square
});

test("transform is applied during rasterization", () => {
  const m = rasterizeShape(rectShape(0, 0, 2, 2), 8, 8, translate(3, 4), 4);
  assert.equal(m.at(3, 4), 1);
  assert.equal(m.at(4, 5), 1);
  assert.equal(m.at(2, 4), 0);
  assert.equal(m.at(5, 5), 0);
});

test("clamp01 bounds values", () => {
  assert.equal(clamp01(-0.5), 0);
  assert.equal(clamp01(1.5), 1);
  assert.equal(clamp01(0.4), 0.4);
});
