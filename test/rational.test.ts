import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational } from "../src/rational.ts";

test("constructs and reduces fractions", () => {
  const r = Rational.of("6/8");
  assert.equal(r.num, 3n);
  assert.equal(r.den, 4n);
  assert.equal(Rational.of("4/-8").toString(), "-1/2");
  assert.equal(Rational.of("-4/-8").toString(), "1/2");
  assert.equal(Rational.of(5).toString(), "5");
});

test("parses decimals and scientific notation exactly", () => {
  assert.equal(Rational.of("0.1").toString(), "1/10");
  assert.equal(Rational.of("-1.25").toString(), "-5/4");
  assert.equal(Rational.of("1.5e2").toString(), "150");
  assert.equal(Rational.of("3e-2").toString(), "3/100");
  assert.equal(Rational.of(0.3).toString(), "3/10");
});

test("arithmetic is exact", () => {
  const third = Rational.of("1/3");
  const sixth = Rational.of("1/6");
  assert.equal(third.add(sixth).toString(), "1/2");
  assert.equal(third.sub(sixth).toString(), "1/6");
  assert.equal(third.mul(sixth).toString(), "1/18");
  assert.equal(third.div(sixth).toString(), "2");
  assert.equal(third.neg().toString(), "-1/3");
  // 0.1 + 0.2 === 0.3 exactly, unlike floats
  assert.ok(Rational.of("0.1").add(Rational.of("0.2")).eq(Rational.of("0.3")));
});

test("comparison and sign", () => {
  assert.equal(Rational.of("1/3").cmp(Rational.of("2/7")), 1);
  assert.equal(Rational.of("2/7").cmp(Rational.of("1/3")), -1);
  assert.equal(Rational.of("2/6").cmp(Rational.of("1/3")), 0);
  assert.equal(Rational.of("-5/2").sign(), -1);
  assert.equal(Rational.of("0/7").sign(), 0);
});

test("rejects invalid input", () => {
  assert.throws(() => Rational.of("1/0"));
  assert.throws(() => Rational.of("abc"));
  assert.throws(() => Rational.of(NaN));
});
