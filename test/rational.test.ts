import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational } from "../src/rational.ts";

test("parse and normalize", () => {
  assert.equal(Rational.parse("2/4").toString(), "1/2");
  assert.equal(Rational.parse("-3/-6").toString(), "1/2");
  assert.equal(Rational.parse("1.25").toString(), "5/4");
  assert.equal(Rational.parse("-0.001").toString(), "-1/1000");
  assert.equal(Rational.parse("1.5e3").toString(), "1500");
  assert.equal(Rational.parse("2.5e-2").toString(), "1/40");
  assert.equal(Rational.parse("0.1").add(Rational.parse("0.2")).toString(), "3/10");
});

test("arithmetic is exact", () => {
  const a = Rational.parse("1/3");
  const b = Rational.parse("1/6");
  assert.equal(a.add(b).toString(), "1/2");
  assert.equal(a.sub(b).toString(), "1/6");
  assert.equal(a.mul(b).toString(), "1/18");
  assert.equal(a.div(b).toString(), "2");
  assert.equal(a.neg().toString(), "-1/3");
  assert.throws(() => a.div(Rational.zero()), /zero/);
});

test("huge values never pass through Number", () => {
  // 40-digit decimals: far beyond float64 / 2^53
  const big = Rational.parse("123456789012345678901234567890.1234567890");
  const doubled = big.add(big);
  assert.equal(doubled.toString(), "123456789012345678901234567890123456789/500000000");
  const one = Rational.parse("0.00000000000000000001"); // 1e-20
  assert.equal(one.mul(one).toString(), "1/1" + "0".repeat(40)); // 1e-20 squared = 1e-40
});

test("exact decimal rendering", () => {
  assert.equal(Rational.parse("1/8").toDecimalString(), "0.125");
  assert.equal(Rational.parse("1/3").toDecimalString(), "1/3"); // non-terminating: stays a fraction
  assert.equal(Rational.parse("-7/2").toDecimalString(), "-3.5");
  assert.equal(Rational.parse("5").toDecimalString(), "5");
});

test("comparison", () => {
  assert.equal(Rational.parse("1/3").cmp(Rational.parse("2/7")), 1);
  assert.equal(Rational.parse("2/6").cmp(Rational.parse("1/3")), 0);
  assert.ok(Rational.parse("-5").cmp(Rational.parse("1/1000")) === -1);
});
