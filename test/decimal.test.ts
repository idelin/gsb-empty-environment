import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ExponentBudgetError,
  canonical,
  compareDecimal,
  integerDigits,
  isIntegerValue,
  isMultipleOf,
  parseDecimal,
  precisionOf,
  scaleOf,
} from "../src/index.ts";

test("0.3 is an integer multiple of 0.1 (no binary float error)", () => {
  assert.equal(isMultipleOf(parseDecimal("0.3"), parseDecimal("0.1")), true);
  // The classic float failure: 0.3 / 0.1 !== 3 in IEEE754.
  assert.notEqual(0.3 / 0.1, 3);
  assert.equal(isMultipleOf(parseDecimal("0.7"), parseDecimal("0.1")), true);
  assert.equal(isMultipleOf(parseDecimal("0.35"), parseDecimal("0.1")), false);
});

test("scientific notation normalizes by definition", () => {
  assert.equal(canonical(parseDecimal("1e3")), "1000");
  assert.equal(canonical(parseDecimal("1.2300e2")), "123");
  assert.equal(canonical(parseDecimal("12.5e-1")), "1.25");
  assert.equal(canonical(parseDecimal("1E-3")), "0.001");
  assert.equal(canonical(parseDecimal("+0.0e+9")), "0");
});

test("1e3 digit count follows the numeric value, not the literal length", () => {
  const d = parseDecimal("1e3");
  assert.equal(precisionOf(d), 4); // value 1000 has 4 digits
  assert.equal(scaleOf(d), 0);
  assert.equal(integerDigits(d), 4);
  assert.equal(precisionOf(parseDecimal("0.1")), 1);
  assert.equal(precisionOf(parseDecimal("1.50")), 2); // trailing zeros stripped
  assert.equal(precisionOf(parseDecimal("0.001")), 3);
});

test("negative zero equals positive zero, raw text preserved", () => {
  const nz = parseDecimal("-0");
  const pz = parseDecimal("0");
  assert.equal(compareDecimal(nz, pz), 0);
  assert.equal(nz.raw, "-0");
  assert.equal(canonical(nz), "0");
  assert.equal(compareDecimal(parseDecimal("-0.00"), parseDecimal("0.0")), 0);
  // number input -0 keeps its sign in raw
  assert.equal(parseDecimal(-0).raw, "-0");
});

test("exact comparison beyond double precision", () => {
  assert.equal(compareDecimal(parseDecimal("0.30000000000000004"), parseDecimal("0.3")), 1);
  assert.equal(compareDecimal(parseDecimal("9007199254740993"), parseDecimal("9007199254740992")), 1);
  assert.equal(compareDecimal(parseDecimal("-5"), parseDecimal("2")), -1);
  assert.equal(compareDecimal(parseDecimal("1e20"), parseDecimal("99999999999999999999")), 1);
});

test("multipleOf with negative and scientific operands", () => {
  assert.equal(isMultipleOf(parseDecimal("-0.3"), parseDecimal("0.1")), true);
  assert.equal(isMultipleOf(parseDecimal("1e3"), parseDecimal("2.5e2")), true);
  assert.equal(isMultipleOf(parseDecimal("7"), parseDecimal("0.25")), true);
  assert.equal(isMultipleOf(parseDecimal("1"), parseDecimal("3")), false);
});

test("integer-valued detection uses the numeric value", () => {
  assert.equal(isIntegerValue(parseDecimal("1e3")), true);
  assert.equal(isIntegerValue(parseDecimal("2.0")), true);
  assert.equal(isIntegerValue(parseDecimal("2.5")), false);
});

test("huge exponents rejected by budget before allocation", () => {
  assert.throws(() => parseDecimal("1e999999999999999999"), ExponentBudgetError);
  assert.throws(() => parseDecimal("1e100001"), ExponentBudgetError);
  assert.throws(() => parseDecimal("1e-100001"), ExponentBudgetError);
  // within budget works
  assert.equal(canonical(parseDecimal("1e1000")), "1" + "0".repeat(1000));
  // custom budget
  assert.throws(() => parseDecimal("1e50", { maxExponent: 10 }), ExponentBudgetError);
  assert.equal(canonical(parseDecimal("1e50", { maxExponent: 100 })), "1" + "0".repeat(50));
});

test("invalid literals rejected", () => {
  for (const bad of ["", "abc", "1.2.3", "e5", "1e", "0x10", "NaN", "Infinity", "--1"]) {
    assert.throws(() => parseDecimal(bad), /invalid decimal literal/);
  }
  assert.throws(() => parseDecimal(NaN), /invalid decimal literal/);
  assert.throws(() => parseDecimal(Infinity), /invalid decimal literal/);
});
