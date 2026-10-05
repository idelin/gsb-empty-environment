import test from 'node:test';
import assert from 'node:assert/strict';
import { Fraction } from '../src/fraction.ts';
import {
  fromPremult8,
  fromStraight8,
  over,
  quantizePremult8,
  quantizeStraight8,
  roundHalfUp,
} from '../src/color.ts';
import { composeGrouped, composeOneShot, resultsDiffer } from '../src/compose.ts';
import { differingGroupings, findMinimalCounterexample, stackToLayers } from '../src/search.ts';

test('Fraction reduces and does exact arithmetic', () => {
  assert.equal(Fraction.of(2, 4).toString(), '1/2');
  assert.equal(Fraction.of(1, 3).add(Fraction.of(1, 6)).toString(), '1/2');
  assert.equal(Fraction.of(1, 255).mul(Fraction.of(255)).toString(), '1');
  assert.equal(Fraction.of(3, 2).cmp(Fraction.of(4, 3)), 1);
  assert.equal(Fraction.of(-2, 4).toString(), '-1/2');
});

test('roundHalfUp rounds halves away from zero, exactly', () => {
  assert.equal(roundHalfUp(Fraction.of(1, 2)), 1);
  assert.equal(roundHalfUp(Fraction.of(3, 2)), 2);
  assert.equal(roundHalfUp(Fraction.of(127, 255)), 0);
  assert.equal(roundHalfUp(Fraction.of(128, 255)), 1);
});

test('zero alpha is handled separately in both formats', () => {
  const straight = fromStraight8({ r: 200, g: 100, b: 50, a: 0 });
  assert.ok(straight.r.isZero() && straight.g.isZero() && straight.b.isZero() && straight.a.isZero());
  assert.deepEqual(quantizeStraight8(straight), { r: 0, g: 0, b: 0, a: 0 });
  const premultTransparent = fromPremult8({ r: 0, g: 0, b: 0, a: 0 });
  assert.deepEqual(quantizeStraight8(premultTransparent), { r: 0, g: 0, b: 0, a: 0 });
});

test('straight to premultiplied conversion is exact', () => {
  const c = fromStraight8({ r: 128, g: 0, b: 255, a: 128 });
  assert.equal(c.r.toString(), '16384/255');
  assert.equal(c.b.toString(), '128');
  assert.equal(c.a.toString(), '128');
});

test('over is associative on exact rationals', () => {
  const a = fromStraight8({ r: 10, g: 200, b: 30, a: 77 });
  const b = fromPremult8({ r: 5, g: 60, b: 2, a: 130 });
  const c = fromStraight8({ r: 255, g: 0, b: 255, a: 3 });
  const left = over(a, over(b, c));
  const right = over(over(a, b), c);
  assert.ok(left.r.equals(right.r) && left.g.equals(right.g));
  assert.ok(left.b.equals(right.b) && left.a.equals(right.a));
});

test('quantizing an 8-bit premultiplied pixel is the identity', () => {
  for (let alpha = 0; alpha <= 255; alpha += 1) {
    for (let v = 0; v <= alpha; v += 1) {
      const px = { r: v, g: v, b: v, a: alpha };
      assert.deepEqual(quantizePremult8(fromPremult8(px)), px);
    }
  }
});

test('two layers can never expose a grouping difference', () => {
  let rng = 0x2f6e2b1;
  const next = (): number => {
    rng = (rng * 1103515245 + 12345) & 0x7fffffff;
    return rng;
  };
  for (let i = 0; i < 20000; i += 1) {
    const mk = () => {
      const a = next() % 256;
      const v = a === 0 ? 0 : next() % (a + 1);
      return { format: 'premultiplied' as const, color: { r: v, g: v, b: v, a } };
    };
    const layers = [mk(), mk()];
    const oneShot = composeOneShot(layers);
    for (const groups of [[2], [1, 1]]) {
      assert.ok(!resultsDiffer(oneShot, composeGrouped(layers, groups)));
    }
  }
});

test('integer predicate agrees with the exact rational pipeline', () => {
  let rng = 0x9e3779b9;
  const next = (): number => {
    rng = (rng * 1103515245 + 12345) & 0x7fffffff;
    return rng;
  };
  for (let i = 0; i < 5000; i += 1) {
    const mk = () => {
      const a = next() % 256;
      return a === 0 ? 0 : next() % (a + 1);
    };
    const stack = { v1: mk(), a1: 0, v2: 0, a2: 0, v3: 0, a3: 0 };
    stack.a1 = Math.max(stack.v1, next() % 256);
    stack.v2 = mk();
    stack.a2 = Math.max(stack.v2, next() % 256);
    stack.v3 = mk();
    stack.a3 = Math.max(stack.v3, next() % 256);
    const expected = differingGroupings(stack);
    const layers = stackToLayers(stack);
    const oneShot = composeOneShot(layers);
    const actual = [[1, 2], [2, 1]].filter((g) => resultsDiffer(oneShot, composeGrouped(layers, g)));
    assert.deepEqual(actual, expected, `mismatch on ${JSON.stringify(stack)}`);
  }
});

test('minimal counterexample: nothing below the bound, witness verified exactly', () => {
  const found = findMinimalCounterexample();
  assert.ok(found);
  assert.equal(found.bound, 1);
  const premultOnly = findMinimalCounterexample(255, false);
  assert.ok(premultOnly);
  assert.equal(premultOnly.bound, 7);
  assert.ok(resultsDiffer(found.oneShot, found.grouped));
  const layers = stackToLayers(found.stack);
  assert.deepEqual(composeOneShot(layers), found.oneShot);
  assert.deepEqual(composeGrouped(layers, found.grouping), found.grouped);
});
