import { test } from "node:test";
import assert from "node:assert/strict";
import { Rational } from "../src/rational.ts";
import { roundRational, roundToInteger } from "../src/rounding.ts";

const r = (s: string, places: number, mode: Parameters<typeof roundRational>[1]["mode"]): string =>
  roundRational(Rational.parse(s), { places, mode }).toString();

test("half-up: ties toward +Infinity", () => {
  assert.equal(r("2.5", 0, "half-up"), "3");
  assert.equal(r("-2.5", 0, "half-up"), "-2");
  assert.equal(r("0.125", 2, "half-up"), "13/100");
});

test("half-down: ties toward -Infinity", () => {
  assert.equal(r("2.5", 0, "half-down"), "2");
  assert.equal(r("-2.5", 0, "half-down"), "-3");
});

test("half-even: banker's rounding", () => {
  assert.equal(r("2.5", 0, "half-even"), "2");
  assert.equal(r("3.5", 0, "half-even"), "4");
  assert.equal(r("-2.5", 0, "half-even"), "-2");
  assert.equal(r("0.125", 2, "half-even"), "3/25");
  assert.equal(r("0.135", 2, "half-even"), "7/50");
});

test("toward-zero / away-from-zero / floor / ceil", () => {
  assert.equal(r("-2.7", 0, "toward-zero"), "-2");
  assert.equal(r("-2.2", 0, "away-from-zero"), "-3");
  assert.equal(r("-2.2", 0, "floor"), "-3");
  assert.equal(r("-2.7", 0, "ceil"), "-2");
  assert.equal(r("2.2", 0, "away-from-zero"), "3");
});

test("non-ties are mode-independent", () => {
  for (const mode of ["half-up", "half-down", "half-even", "floor", "ceil"] as const) {
    // 0.1249 < 0.125 for every tie-handling mode except ceil
    if (mode !== "ceil") assert.equal(r("0.1249", 2, mode), "3/25");
  }
});

test("negative places round to tens/hundreds", () => {
  assert.equal(r("12345", -2, "half-up"), "12300");
  assert.equal(r("12350", -2, "half-up"), "12400");
  assert.equal(r("12350", -2, "half-even"), "12400");
  assert.equal(r("12250", -2, "half-even"), "12200");
});

test("rounding is exact on rationals (no float detour)", () => {
  // 1/3 rounded to 2 places: exact 1/3 vs float 0.33... would differ at 20 places
  const third = Rational.parse("1/3");
  assert.equal(roundRational(third, { places: 2, mode: "half-up" }).toString(), "33/100");
  assert.equal(roundRational(third, { places: 20, mode: "half-up" }).toString(),
    "33333333333333333333/100000000000000000000");
  assert.equal(roundToInteger(Rational.parse("-7/2"), "half-even"), -4n);
});
