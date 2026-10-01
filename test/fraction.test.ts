import { test } from "node:test";
import assert from "node:assert/strict";
import { expandCycle, FractionExpander, verifyFractionDigits } from "../src/fraction.ts";
import { Rational } from "../src/rational.ts";

function expand(num: bigint, den: bigint, base: number, budget = Infinity) {
  const r = new Rational(num, den);
  const { rem, den: d } = new Rational(r.num < 0n ? -r.num : r.num, r.den).split();
  const ex = new FractionExpander(rem, d, base, "lower", budget);
  ex.run();
  return ex;
}

test("1/3 in base 10 is 0.(3)", () => {
  const ex = expand(1n, 3n, 10);
  assert.equal(ex.getStatus(), "cycle");
  assert.equal(ex.getPrefix(), "");
  assert.equal(ex.getCycle(), "3");
});

test("1/2 in base 10 terminates as 0.5", () => {
  const ex = expand(1n, 2n, 10);
  assert.equal(ex.getStatus(), "terminated");
  assert.equal(ex.getDigits(), "5");
  assert.equal(ex.getCycle(), "");
});

test("1/7 in base 10 has cycle 142857", () => {
  const ex = expand(1n, 7n, 10);
  assert.equal(ex.getStatus(), "cycle");
  assert.equal(ex.getCycle(), "142857");
});

test("decimal 0.1 in binary is 0.0(0011) and STOPS (no infinite output)", () => {
  const ex = expand(1n, 10n, 2);
  assert.equal(ex.getStatus(), "cycle");
  assert.equal(ex.getPrefix(), "0");
  assert.equal(ex.getCycle(), "0011");
  assert.ok(ex.getSteps() <= 10, "cycle found within denominator bound");
});

test("budget exhaustion is explicitly marked incomplete", () => {
  const ex = expand(1n, 7n, 10, 3);
  assert.equal(ex.getStatus(), "budget-exhausted");
  assert.equal(ex.getDigits(), "142");
  assert.equal(ex.getCycle(), "", "no cycle claim when budget cut the search");
});

test("budget exactly at cycle boundary completes", () => {
  const ex = expand(1n, 7n, 10, 6);
  assert.equal(ex.getStatus(), "cycle");
  assert.equal(ex.getCycle(), "142857");
});

test("pause / resume / snapshot / restore reproduces the uninterrupted run", () => {
  const full = expand(1n, 97n, 10);
  const fullDigits = full.getDigits();
  const r = new Rational(1n, 97n);
  const ex = new FractionExpander(1n, r.den, 10, "lower");
  ex.run(13);
  ex.pause();
  assert.equal(ex.getStatus(), "paused");
  assert.equal(ex.getDigits(), fullDigits.slice(0, 13));
  const snap = JSON.parse(JSON.stringify(ex.snapshot()));
  const ex2 = FractionExpander.restore(snap, r.den, 10, "lower");
  ex2.resume();
  ex2.run();
  assert.equal(ex2.getStatus(), "cycle");
  assert.equal(ex2.getDigits(), fullDigits);
  assert.equal(ex2.getPrefix(), full.getPrefix());
  assert.equal(ex2.getCycle(), full.getCycle());
});

test("independent verification accepts the true cycle", () => {
  const v = verifyFractionDigits(new Rational(1n, 7n), 10, "lower", "", "142857");
  assert.equal(v.valid, true);
  assert.equal(v.minimalCycle, true);
  assert.equal(v.canonicalStart, true);
});

test("independent verification rejects a tampered cycle", () => {
  const v = verifyFractionDigits(new Rational(1n, 7n), 10, "lower", "", "142858");
  assert.equal(v.valid, false);
  assert.match(v.reason, /value mismatch/);
});

test("verification flags non-minimal cycle", () => {
  const v = verifyFractionDigits(new Rational(1n, 3n), 10, "lower", "", "33");
  assert.equal(v.valid, true, "value is still correct");
  assert.equal(v.minimalCycle, false);
});

test("verification flags non-canonical start (cycle shiftable into prefix)", () => {
  const v = verifyFractionDigits(new Rational(1n, 3n), 10, "lower", "3", "3");
  assert.equal(v.valid, true);
  assert.equal(v.canonicalStart, false);
});

test("verification handles terminating expansions (empty cycle)", () => {
  const v = verifyFractionDigits(new Rational(1n, 2n), 10, "lower", "5", "");
  assert.equal(v.valid, true);
});

test("verification rejects out-of-range digits", () => {
  const v = verifyFractionDigits(new Rational(1n, 3n), 10, "lower", "", "a");
  assert.equal(v.valid, false);
});

test("expandCycle re-expands deterministically", () => {
  assert.equal(expandCycle("0", "0011", 9), "000110011");
  assert.equal(expandCycle("142", "857", 4), "1428");
});
