import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational } from "../src/rational.ts";
import { SI_TABLE, toBase, fromBase, dimsMul, dimsInv, dimsKey } from "../src/units.ts";

test("exact conversions", () => {
  assert.equal(toBase(SI_TABLE, Rational.parse("1"), "km").value.toString(), "1000");
  assert.equal(toBase(SI_TABLE, Rational.parse("1"), "in").value.toString(), "127/5000");
  assert.equal(fromBase(SI_TABLE, Rational.parse("127/5000"), "cm").toString(), "127/50");
  assert.equal(toBase(SI_TABLE, Rational.parse("2"), "h").value.toString(), "7200");
});

test("dimension algebra", () => {
  const v = dimsMul({ m: 1 }, { s: -1 });
  assert.equal(dimsKey(v), "m^1*s^-1");
  assert.equal(dimsKey(dimsMul(v, dimsInv(v))), "1");
  assert.equal(dimsKey(dimsMul({ m: 1 }, { m: 1 })), "m^2");
});

test("L is exactly 1/1000 m^3", () => {
  const b = toBase(SI_TABLE, Rational.parse("1"), "L");
  assert.equal(b.value.toString(), "1/1000");
  assert.equal(dimsKey(b.dims), "m^3");
});
