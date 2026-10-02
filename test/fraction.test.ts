import { test } from "node:test";
import assert from "node:assert/strict";
import { Fraction } from "../src/fraction.ts";

test("construction normalizes sign and reduces", () => {
  const f = new Fraction(6n, -8n);
  assert.equal(f.num, -3n);
  assert.equal(f.den, 4n);
  assert.equal(f.toString(), "-3/4");
});

test("parse from string and integer", () => {
  assert.equal(Fraction.from("3/4").toString(), "3/4");
  assert.equal(Fraction.from("-5").toString(), "-5");
  assert.equal(Fraction.from(7).toString(), "7");
  assert.throws(() => Fraction.from(0.5));
});

test("arithmetic is exact", () => {
  const a = Fraction.from("1/3");
  const b = Fraction.from("1/6");
  assert.equal(a.add(b).toString(), "1/2");
  assert.equal(a.sub(b).toString(), "1/6");
  assert.equal(a.mul(b).toString(), "1/18");
  assert.equal(a.div(b).toString(), "2");
  assert.equal(a.neg().toString(), "-1/3");
});

test("comparison", () => {
  assert.equal(Fraction.from("2/3").cmp(Fraction.from("3/4")), -1);
  assert.equal(Fraction.from("2/4").cmp(Fraction.from("1/2")), 0);
  assert.ok(Fraction.from("5/2").eq(Fraction.from("10/4")));
});
