import { test } from "node:test";
import assert from "node:assert/strict";
import { Fraction, frac } from "../src/fraction.ts";

test("construction normalizes sign and reduces by gcd", () => {
  assert.equal(new Fraction(2n, 4n).toString(), "1/2");
  assert.equal(new Fraction(-2n, 4n).toString(), "-1/2");
  assert.equal(new Fraction(2n, -4n).toString(), "-1/2");
  assert.equal(new Fraction(-2n, -4n).toString(), "1/2");
  assert.equal(new Fraction(0n, 7n).toString(), "0");
  assert.equal(new Fraction(6n, 1n).toString(), "6");
});

test("rejects a zero denominator", () => {
  assert.throws(() => new Fraction(1n, 0n), /denominator/);
  assert.throws(() => frac("1/0"), /denominator/);
});

test("parses integers, fractions and decimals exactly", () => {
  assert.equal(frac("3/4").toString(), "3/4");
  assert.equal(frac("-6/8").toString(), "-3/4");
  assert.equal(frac(" 5 ").toString(), "5");
  assert.equal(frac(7n).toString(), "7");
  assert.equal(frac(-2).toString(), "-2");
  assert.equal(frac("0.125").toString(), "1/8");
  assert.equal(frac("-2.5").toString(), "-5/2");
  assert.equal(frac(".75").toString(), "3/4");
});

test("rejects non-integer numbers to avoid float contamination", () => {
  assert.throws(() => frac(0.1), /safe integer/);
});

test("arithmetic is exact", () => {
  assert.equal(frac("1/3").add("1/6").toString(), "1/2");
  assert.equal(frac("1/3").sub("1/2").toString(), "-1/6");
  assert.equal(frac("2/3").mul("9/4").toString(), "3/2");
  assert.equal(frac("2/3").div("4/5").toString(), "5/6");
  assert.equal(frac("2/3").neg().toString(), "-2/3");
  assert.equal(frac("-2/3").abs().toString(), "2/3");
  // No accumulation error: 1/3 + 1/3 + 1/3 === 1 exactly.
  assert.equal(frac("1/3").add("1/3").add("1/3").toString(), "1");
});

test("division by zero throws", () => {
  assert.throws(() => frac(1).div(0), /division by zero/);
});

test("comparisons and predicates", () => {
  assert.equal(frac("1/3").compare("2/7"), 1);
  assert.equal(frac("2/7").compare("1/3"), -1);
  assert.equal(frac("2/4").compare("1/2"), 0);
  assert.ok(frac("1/2").equals("2/4"));
  assert.ok(frac(0).isZero());
  assert.ok(frac("1/10").isPositive());
  assert.ok(frac("-1/10").isNegative());
});

test("huge exact values stay exact (beyond float64 precision)", () => {
  const big = frac("123456789123456789/987654321987654321");
  const doubled = big.add(big);
  assert.equal(doubled.toString(), "27434842/109739369");
  assert.equal(frac("1/10").add("2/10").toString(), "3/10"); // 0.1 + 0.2 !== 0.3 in floats
});
