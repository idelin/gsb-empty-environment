import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational, rat } from "../src/rational.ts";

test("exact arithmetic: 1/3 + 1/6 = 1/2", () => {
  const sum = Rational.of(1n, 3n).add(Rational.of(1n, 6n));
  assert.equal(sum.toString(), "1/2");
});

test("exact arithmetic: normalisation and sign", () => {
  assert.equal(Rational.of(2n, 4n).toString(), "1/2");
  assert.equal(Rational.of(-2n, 4n).toString(), "-1/2");
  assert.equal(Rational.of(2n, -4n).toString(), "-1/2");
  assert.equal(Rational.of(-2n, -4n).toString(), "1/2");
});

test("big integers beyond 2^53 stay exact (no Number transit)", () => {
  // 2^70 + 1 is not representable as a Number.
  const big = (1n << 70n) + 1n;
  const r = Rational.of(big, 3n);
  const back = r.mul(Rational.of(3n));
  assert.equal(back.num, big);
  assert.equal(back.den, 1n);
  // A Number transit would corrupt this comparison.
  const a = Rational.of((1n << 60n) + 1n);
  const b = Rational.of(1n << 60n);
  assert.ok(a.sub(b).eq(Rational.one()));
});

test("parse: decimals and scientific notation are exact", () => {
  assert.equal(rat("0.1").toString(), "1/10");
  assert.equal(rat("-3.25").toString(), "-13/4");
  assert.equal(rat("1.5e-3").toString(), "3/2000");
  assert.equal(rat("1.25e3").toString(), "1250");
  assert.equal(rat("9007199254740993").toString(), "9007199254740993");
  assert.throws(() => rat("1.2.3"));
});

test("comparison and sign", () => {
  assert.ok(rat("0.3333333334").gt(rat("0.3333333333")));
  assert.equal(rat("0").sign(), 0);
  assert.equal(rat("-2").sign(), -1);
  assert.ok(rat("2").gte(rat("2")));
});

test("explicit rounding modes on 1/8 = 0.125 at scale 2", () => {
  const v = rat("0.125");
  assert.equal(v.toDecimal(2, "floor"), "0.12");
  assert.equal(v.toDecimal(2, "ceil"), "0.13");
  assert.equal(v.toDecimal(2, "towardZero"), "0.12");
  assert.equal(v.toDecimal(2, "awayFromZero"), "0.13");
  assert.equal(v.toDecimal(2, "halfUp"), "0.13");
  assert.equal(v.toDecimal(2, "halfEven"), "0.12"); // 2 is even
});

test("explicit rounding modes on -1/8 = -0.125 at scale 2", () => {
  const v = rat("-0.125");
  assert.equal(v.toDecimal(2, "floor"), "-0.13");
  assert.equal(v.toDecimal(2, "ceil"), "-0.12");
  assert.equal(v.toDecimal(2, "towardZero"), "-0.12");
  assert.equal(v.toDecimal(2, "awayFromZero"), "-0.13");
  assert.equal(v.toDecimal(2, "halfUp"), "-0.13");
  assert.equal(v.toDecimal(2, "halfEven"), "-0.12");
});

test("halfEven rounds to even on exact ties only", () => {
  assert.equal(rat("0.375").toDecimal(2, "halfEven"), "0.38"); // 7 odd -> 8
  assert.equal(rat("0.3751").toDecimal(2, "halfEven"), "0.38"); // above tie
  assert.equal(rat("0.3749").toDecimal(2, "halfEven"), "0.37"); // below tie
});

test("exact values render without rounding", () => {
  assert.equal(rat("1.5").toDecimal(2, "floor"), "1.50");
  assert.equal(rat("1.5").toDecimal(0, "ceil"), "2");
  assert.equal(rat("2").toDecimal(0, "floor"), "2");
  assert.equal(rat("-0.001").toDecimal(2, "floor"), "-0.01");
  assert.equal(rat("-0.001").toDecimal(2, "ceil"), "0.00");
});

test("division by zero and zero denominator are rejected", () => {
  assert.throws(() => Rational.of(1n, 0n));
  assert.throws(() => rat("1").div(rat("0")));
});
