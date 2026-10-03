import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DecimalBudgetError,
  DecimalParseError,
  compare,
  fractionDigits,
  isMultipleOf,
  parseDecimal,
  toPlainString,
  toScientificString,
  totalDigits,
} from "../src/decimal.ts";

test("parses and normalizes plain decimals", () => {
  const d = parseDecimal("001.2300");
  assert.equal(d.sign, 1);
  assert.equal(d.coeff, 123n);
  assert.equal(d.scale, 2);
  assert.equal(toPlainString(d), "1.23");
});

test("normalizes scientific notation by definition", () => {
  const d = parseDecimal("1e3");
  assert.equal(d.coeff, 1n);
  assert.equal(d.scale, -3);
  assert.equal(toPlainString(d), "1000");
  assert.equal(toScientificString(d), "1e+3");
  assert.equal(toScientificString(parseDecimal("12.34e-1")), "1.234e+0");
  assert.equal(toScientificString(parseDecimal("0.05")), "5e-2");
});

test("1e3 digit count is numeric (4), not textual (3)", () => {
  assert.equal(totalDigits(parseDecimal("1e3")), 4);
  assert.equal(totalDigits(parseDecimal("0.30")), 1);
  assert.equal(totalDigits(parseDecimal("123.45")), 5);
  assert.equal(totalDigits(parseDecimal("0")), 1);
  assert.equal(fractionDigits(parseDecimal("1e3")), 0);
  assert.equal(fractionDigits(parseDecimal("0.030")), 2);
});

test("negative zero equals positive zero, original text preserved", () => {
  const neg = parseDecimal("-0");
  const pos = parseDecimal("0.00");
  assert.equal(compare(neg, pos), 0);
  assert.equal(neg.sign, 0);
  assert.equal(neg.text, "-0");
  assert.equal(toPlainString(neg), "0");
});

test("0.3 is an integer multiple of 0.1 (no binary float error)", () => {
  // IEEE-754: 0.3 / 0.1 === 2.9999999999999996
  assert.ok(isMultipleOf(parseDecimal("0.3"), parseDecimal("0.1")));
  assert.ok(isMultipleOf(parseDecimal("-0.3"), parseDecimal("0.1")));
  assert.ok(isMultipleOf(parseDecimal("0"), parseDecimal("0.1")));
  assert.ok(!isMultipleOf(parseDecimal("0.35"), parseDecimal("0.1")));
  assert.ok(isMultipleOf(parseDecimal("2"), parseDecimal("0.5")));
  assert.ok(isMultipleOf(parseDecimal("1e3"), parseDecimal("1e-3")));
});

test("negative exponents and bounds never touch floats", () => {
  const d = parseDecimal("-2.5e-4");
  assert.equal(d.coeff, 25n);
  assert.equal(d.scale, 5);
  assert.equal(toPlainString(d), "-0.00025");
});

test("huge exponents are rejected by budget before allocating zeros", () => {
  assert.throws(() => parseDecimal("1e999999999999999999"), DecimalBudgetError);
  assert.throws(() => parseDecimal("1e-999999999999999999"), DecimalBudgetError);
  // A custom budget still allows moderate values and rejects beyond it.
  assert.equal(toPlainString(parseDecimal("1e20", { maxScale: 100 })), "1" + "0".repeat(20));
  assert.throws(() => parseDecimal("1e101", { maxScale: 100 }), DecimalBudgetError);
});

test("budget rejection is fast (no exponential work)", () => {
  const start = Date.now();
  assert.throws(() => parseDecimal("9".repeat(40) + "e" + "9".repeat(30)), DecimalBudgetError);
  assert.ok(Date.now() - start < 500);
});

test("invalid literals are parse errors", () => {
  for (const bad of ["", "abc", "1.2.3", "1e", "1e+", ".5", "1.", "+", "0x10", "1_000"]) {
    assert.throws(() => parseDecimal(bad), DecimalParseError, bad);
  }
});

test("comparison is exact across scales and signs", () => {
  assert.equal(compare(parseDecimal("0.1"), parseDecimal("0.10")), 0);
  assert.ok(compare(parseDecimal("-1e-3"), parseDecimal("-0.001")) === 0);
  assert.ok(compare(parseDecimal("2"), parseDecimal("-2")) > 0);
  assert.ok(compare(parseDecimal("0.999"), parseDecimal("1")) < 0);
  assert.ok(compare(parseDecimal("1e-3"), parseDecimal("0.001")) === 0);
});
