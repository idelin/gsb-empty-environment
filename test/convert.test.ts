import { test } from "node:test";
import assert from "node:assert/strict";
import { convert, formatRational, parseRational, InvalidDigitError } from "../src/convert.ts";
import { Rational } from "../src/rational.ts";

test("negative zero parses to canonical zero and never renders a minus", () => {
  for (const s of ["-0", "-0.0", "-0.(0)", "0", "0.000"]) {
    const r = parseRational(s, 10);
    assert.equal(r.num, 0n);
    assert.equal(r.den, 1n);
    assert.equal(formatRational(r, { base: 10 }).text, "0");
  }
  // but a real negative keeps its sign
  assert.equal(formatRational(parseRational("-0.5", 10), { base: 10 }).text, "-0.5");
});

test("decimal 0.1 -> binary is 0.0(0011), complete, finite output", () => {
  const out = convert("0.1", { fromBase: 10, base: 2 });
  assert.equal(out.text, "0.0(0011)");
  assert.equal(out.complete, true);
  assert.equal(out.report.status, "cycle");
  assert.ok(out.report.digitsEmitted <= 6);
});

test("round-trips a repeating binary value back to decimal", () => {
  const out = convert("0.0(0011)", { fromBase: 2, base: 10 });
  assert.equal(out.text, "0.1");
});

test("invalid digit error carries the numeral-wide position", () => {
  try {
    parseRational("12.3a5", 10);
    assert.fail("should throw");
  } catch (e) {
    const err = e as InvalidDigitError;
    assert.ok(err instanceof InvalidDigitError);
    assert.equal(err.position, 4);
    assert.equal(err.char, "a");
  }
  // inside the cycle part: "-1.0(0x)" -> 'x' at index 6
  try {
    parseRational("-1.0(0x)", 10);
    assert.fail("should throw");
  } catch (e) {
    assert.equal((e as InvalidDigitError).position, 6);
  }
});

test("budget truncation is marked incomplete; truncation vs nearest rounding", () => {
  // 1/7 = 0.(142857): the 6-digit cycle exceeds the budget, so the cut is real.
  const t = formatRational(new Rational(1n, 7n), { base: 10, budget: 3 });
  assert.equal(t.text, "0.142");
  assert.equal(t.complete, false);
  assert.equal(t.report.status, "budget-exhausted");
  assert.equal(t.report.budget, 3);
  assert.equal(t.report.budgetUsed, 3);
  assert.equal(t.report.rounded, false);

  const n = formatRational(new Rational(1n, 7n), { base: 10, budget: 3, rounding: "nearest" });
  assert.equal(n.text, "0.143");
  assert.equal(n.complete, false);
  assert.equal(n.report.rounded, true);
});

test("nearest rounding carries into the integer part", () => {
  const n = formatRational(new Rational(9999n, 1000n), { base: 10, budget: 2, rounding: "nearest" });
  assert.equal(n.text, "10.00");
  assert.equal(n.report.rounded, true);
});

test("nearest rounding does not round when next digit is below half", () => {
  // 1/7 = 0.142857... budget 2 -> next digit 2 < 5, stays 0.14
  const n = formatRational(new Rational(1n, 7n), { base: 10, budget: 2, rounding: "nearest" });
  assert.equal(n.text, "0.14");
  assert.equal(n.report.rounded, false);
});

test("integers emit no fraction and report integer status", () => {
  const out = formatRational(new Rational(255n), { base: 16 });
  assert.equal(out.text, "ff");
  assert.equal(out.report.status, "terminated");
  assert.equal(out.complete, true);
});

test("big integer conversion reports chunked workload", () => {
  const big = "9".repeat(500);
  const out = convert(big, { fromBase: 10, base: 16 });
  assert.equal(out.text, parseRational(big, 10).num.toString(16));
  assert.ok(out.report.integerDivisions > 0);
  assert.ok(out.report.integerDivisions < 100, `divisions: ${out.report.integerDivisions}`);
});

test("upper-case charset output and parse-back", () => {
  const out = formatRational(new Rational(255n), { base: 16, charset: "upper" });
  assert.equal(out.text, "FF");
  const back = parseRational("FF", 16, "upper");
  assert.equal(back.num, 255n);
  // wrong case rejected
  assert.throws(() => parseRational("ff", 16, "upper"), InvalidDigitError);
});

test("base62 round-trip with explicit charset", () => {
  const v = new Rational(123456789012345678901234567890n, 997n);
  const out = formatRational(v, { base: 62, charset: "base62" });
  const back = parseRational(out.text, 62, "base62");
  assert.ok(back.equals(v));
});

test("malformed numerals are rejected", () => {
  for (const s of ["", ".", "1..2", "1.2.3", "(1)", "1(2)", "--1", "1.", "1.2(", "1.2)"]) {
    assert.throws(() => parseRational(s, 10), undefined, JSON.stringify(s));
  }
});

test("empty-prefix cycle notation parses: 1.(2) = 11/9, 0.(3) = 1/3", () => {
  assert.ok(parseRational("1.(2)", 10).equals(new Rational(11n, 9n)));
  assert.ok(parseRational("0.(3)", 10).equals(new Rational(1n, 3n)));
  assert.ok(parseRational("-0.(0)", 10).isZero);
});
