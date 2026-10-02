import { test } from "node:test";
import assert from "node:assert/strict";
import { Rat } from "../src/rational.ts";
import {
  evalInt,
  intToRatPoly,
  isolateRoots,
  polyToString,
  refineRoot,
  sturmSequence,
} from "../src/poly.ts";

const half = Rat.of(1n, 2n);
const third = Rat.of(1n, 3n);

test("polynomial evaluation is exact", () => {
  // p = 2t^2 - 1 at t = 1/2 -> -1/2
  assert.equal(evalInt([-1n, 0n, 2n], half).toString(), "-1/2");
});

test("polyToString renders descending terms", () => {
  assert.equal(polyToString([4n, -8n]), "-8t + 4");
  assert.equal(polyToString([0n, 0n, 1n]), "t^2");
  assert.equal(polyToString([]), "0");
});

test("isolates two rational roots of 6t^2 - 5t + 1 in (0,1)", () => {
  const p = intToRatPoly([1n, -5n, 6n]); // (2t-1)(3t-1)
  const roots = isolateRoots(p, Rat.of(0n), Rat.of(1n));
  assert.equal(roots.length, 2);
  const seq = sturmSequence(p);
  const refined = roots.map((r) => refineRoot(seq, r, 40));
  // 1/3 and 1/2 lie inside the certified intervals (or are found exactly).
  for (const [root, expected] of refined.map((r, i) => [r, [third, half][i]] as const)) {
    const lo = root.exact ?? root.lo;
    const hi = root.exact ?? root.hi;
    assert.ok(lo.cmp(expected) <= 0 && hi.cmp(expected) >= 0, `root near ${expected}`);
  }
});

test("finds exact rational root 1/2 of 2t - 1", () => {
  const roots = isolateRoots(intToRatPoly([-1n, 2n]), Rat.of(0n), Rat.of(1n));
  assert.equal(roots.length, 1);
  const refined = refineRoot(sturmSequence(intToRatPoly([-1n, 2n])), roots[0], 40);
  assert.equal(refined.exact?.toString(), "1/2");
});

test("double root (2t-1)^2 is isolated once, as a distinct root", () => {
  const p = intToRatPoly([1n, -4n, 4n]);
  const roots = isolateRoots(p, Rat.of(0n), Rat.of(1n));
  assert.equal(roots.length, 1);
  const refined = refineRoot(sturmSequence(p), roots[0], 40);
  assert.equal(refined.exact?.toString(), "1/2");
});

test("isolates irrational root sqrt(2)/2 of 2t^2 - 1 with certified width", () => {
  const p = intToRatPoly([-1n, 0n, 2n]);
  const roots = isolateRoots(p, Rat.of(0n), Rat.of(1n));
  assert.equal(roots.length, 1);
  const refined = refineRoot(sturmSequence(p), roots[0], 48);
  assert.equal(refined.exact, null);
  const sqrt2over2 = Math.SQRT1_2;
  assert.ok(refined.lo.toNumber() <= sqrt2over2 && refined.hi.toNumber() >= sqrt2over2);
  assert.ok(refined.hi.sub(refined.lo).toNumber() < 2 ** -48);
});
