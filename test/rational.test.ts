import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational, rmax, rmin } from "../src/rational.ts";

test("normalizes sign and reduces", () => {
  const a = Rational.of(2, -4);
  assert.equal(a.num, -1n);
  assert.equal(a.den, 2n);
  assert.equal(Rational.of(6, 9).toString(), "2/3");
  assert.equal(Rational.of(-6, -9).toString(), "2/3");
  assert.equal(Rational.of(10, 5).toString(), "2");
});

test("arithmetic is exact", () => {
  const a = Rational.of(1, 3);
  const b = Rational.of(1, 6);
  assert.ok(a.add(b).eq(Rational.of(1, 2)));
  assert.ok(a.sub(b).eq(Rational.of(1, 6)));
  assert.ok(a.mul(b).eq(Rational.of(1, 18)));
  assert.ok(a.div(b).eq(Rational.of(2)));
  assert.ok(a.neg().eq(Rational.of(-1, 3)));
});

test("comparisons and min/max", () => {
  const a = Rational.of(2, 3);
  const b = Rational.of(5, 7);
  assert.equal(a.cmp(b), -1);
  assert.ok(a.lt(b));
  assert.ok(b.gt(a));
  assert.ok(a.lte(Rational.of(4, 6)));
  assert.ok(a.gte(Rational.of(4, 6)));
  assert.ok(rmin(a, b).eq(a));
  assert.ok(rmax(a, b).eq(b));
});

test("rejects zero denominator and division by zero", () => {
  assert.throws(() => Rational.of(1, 0));
  assert.throws(() => Rational.of(1).div(Rational.zero()));
});
