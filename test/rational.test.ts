import { test } from "node:test";
import assert from "node:assert/strict";
import * as R from "../src/rational.ts";

test("parse is exact and never routes through Number", () => {
  const big = R.parse("123456789012345678901234567890.000000001");
  assert.equal(R.toCanonical(big), "123456789012345678901234567890000000001/1000000000");
  // 2^70 is not exactly representable-adjacent territory for Number; BigInt keeps it exact.
  const pow = R.parse("1180591620717411303424"); // 2^70
  assert.equal(R.toCanonical(pow), "1180591620717411303424");
  assert.equal(R.toCanonical(R.parse("7/3")), "7/3");
  assert.equal(R.toCanonical(R.parse("-0.5")), "-1/2");
  assert.equal(R.toCanonical(R.parse("2.5/5")), "1/2");
  assert.throws(() => R.parse("1/0"));
  assert.throws(() => R.parse("abc"));
});

test("arithmetic is exact and normalized", () => {
  const a = R.parse("0.1");
  const b = R.parse("0.2");
  assert.equal(R.toCanonical(R.add(a, b)), "3/10"); // not 0.30000000000000004
  assert.equal(R.toCanonical(R.mul(R.parse("-2/3"), R.parse("9/4"))), "-3/2");
  assert.equal(R.toCanonical(R.div(R.parse("1"), R.parse("3"))), "1/3");
  assert.equal(R.toCanonical(R.sub(R.parse("1"), R.parse("0.999999999"))), "1/1000000000");
});

test("comparison and abs", () => {
  assert.equal(R.cmp(R.parse("-1/3"), R.parse("-0.33")), -1);
  assert.equal(R.toCanonical(R.abs(R.parse("-5/7"))), "5/7");
  assert.ok(R.eq(R.parse("0.5"), R.parse("1/2")));
});

test("toDecimal rounding is explicit (half-even default)", () => {
  assert.equal(R.toDecimal(R.parse("1/3"), 6), "0.333333");
  assert.equal(R.toDecimal(R.parse("2/3"), 6), "0.666667");
  assert.equal(R.toDecimal(R.parse("0.0000005"), 6), "0.000000"); // half-even to even 0
  assert.equal(R.toDecimal(R.parse("0.0000015"), 6), "0.000002"); // half-even to even 2
  assert.equal(R.toDecimal(R.parse("0.0000005"), 6, "half-up"), "0.000001");
  assert.equal(R.toDecimal(R.parse("-0.0000005"), 6, "down"), "0.000000");
  assert.equal(R.toDecimal(R.parse("-0.0000005"), 6, "up"), "-0.000001");
  assert.equal(R.toDecimal(R.parse("123456789012345678901234567890"), 6), "123456789012345678901234567890.000000");
});
