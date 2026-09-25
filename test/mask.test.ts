import { test } from 'node:test';
import assert from 'node:assert/strict';
import { identity, TransformStack } from '../src/matrix.ts';
import { PathBuilder } from '../src/path.ts';
import {
  applyMask,
  createMask,
  fillPath,
  invertMask,
  maskDifference,
  maskIntersect,
  maskUnion,
} from '../src/raster.ts';

function softMask(seed: number): ReturnType<typeof createMask> {
  const mask = createMask(6, 5);
  for (let i = 0; i < mask.data.length; i++) {
    mask.data[i] = ((i * 7 + seed * 13) % 17) / 16;
  }
  return mask;
}

test('boolean union/intersect/difference follow coverage algebra and stay in [0,1]', () => {
  const a = softMask(1);
  const b = softMask(2);
  const union = maskUnion(a, b);
  const intersect = maskIntersect(a, b);
  const difference = maskDifference(a, b);
  for (let i = 0; i < a.data.length; i++) {
    const x = a.data[i];
    const y = b.data[i];
    assert.ok(Math.abs(union.data[i] - (x + y - x * y)) < 1e-6);
    assert.ok(Math.abs(intersect.data[i] - x * y) < 1e-6);
    assert.ok(Math.abs(difference.data[i] - x * (1 - y)) < 1e-6);
    for (const v of [union.data[i], intersect.data[i], difference.data[i]]) {
      assert.ok(v >= 0 && v <= 1, `value ${v} out of [0,1]`);
    }
  }
});

test('boolean ops on hard masks reduce to set semantics', () => {
  const rectA = new PathBuilder().rect(1, 1, 4, 3).flatten(identity());
  const rectB = new PathBuilder().rect(3, 2, 4, 3).flatten(identity());
  const a = fillPath(rectA, 8, 8);
  const b = fillPath(rectB, 8, 8);
  const union = maskUnion(a, b);
  const intersect = maskIntersect(a, b);
  const difference = maskDifference(a, b);
  for (let i = 0; i < a.data.length; i++) {
    assert.equal(union.data[i], Math.max(a.data[i], b.data[i]));
    assert.equal(intersect.data[i], Math.min(a.data[i], b.data[i]));
    assert.equal(difference.data[i], a.data[i] * (1 - b.data[i]));
  }
  assert.equal(union.data[1 * 8 + 1], 1);
  assert.equal(union.data[4 * 8 + 6], 1);
  assert.equal(intersect.data[2 * 8 + 3], 1);
  assert.equal(intersect.data[1 * 8 + 1], 0);
  assert.equal(difference.data[1 * 8 + 1], 1);
  assert.equal(difference.data[2 * 8 + 3], 0);
});

test('inverse mask complements only inside the specified region', () => {
  const mask = fillPath(new PathBuilder().rect(2, 2, 2, 2).flatten(identity()), 8, 8);
  const region = { x: 1, y: 1, width: 4, height: 4 };
  const inverse = invertMask(mask, region);
  assert.equal(inverse.data[0], 0);
  assert.equal(inverse.data[6 * 8 + 6], 0);
  assert.equal(inverse.data[1 * 8 + 1], 1);
  assert.equal(inverse.data[2 * 8 + 2], 0);
  assert.equal(inverse.data[4 * 8 + 4], 1);
  for (const v of inverse.data) assert.ok(v >= 0 && v <= 1);
});

test('double inversion restores the original mask inside the region', () => {
  const stack = new TransformStack();
  stack.translate(4, 4);
  stack.rotate(Math.PI / 7);
  const mask = fillPath(new PathBuilder().rect(-2.5, -1.5, 5, 3).flatten(stack.current), 8, 8);
  const region = { x: 0, y: 0, width: 8, height: 8 };
  const twice = invertMask(invertMask(mask, region), region);
  for (let i = 0; i < mask.data.length; i++) {
    assert.ok(Math.abs(twice.data[i] - mask.data[i]) < 1e-6);
  }
});

test('De Morgan: invert(union) equals intersect(invert, invert) within the region', () => {
  const a = softMask(3);
  const b = softMask(4);
  const region = { x: 0, y: 0, width: 6, height: 5 };
  const lhs = invertMask(maskUnion(a, b), region);
  const rhs = maskIntersect(invertMask(a, region), invertMask(b, region));
  for (let i = 0; i < lhs.data.length; i++) {
    assert.ok(Math.abs(lhs.data[i] - rhs.data[i]) < 1e-6);
  }
});

test('repeated mask application is multiplicative per the mathematical definition', () => {
  const base = softMask(5);
  const m1 = softMask(6);
  const m2 = softMask(7);
  const sequential = applyMask(applyMask(base, m1), m2);
  const chained = applyMask(base, m1, m2);
  const twice = applyMask(base, m1, m1);
  for (let i = 0; i < base.data.length; i++) {
    assert.ok(Math.abs(sequential.data[i] - chained.data[i]) < 1e-6);
    assert.ok(Math.abs(chained.data[i] - base.data[i] * m1.data[i] * m2.data[i]) < 1e-6);
    assert.ok(Math.abs(twice.data[i] - base.data[i] * m1.data[i] * m1.data[i]) < 1e-6);
    assert.ok(chained.data[i] >= 0 && chained.data[i] <= 1);
  }
});
