import { test } from "node:test";
import assert from "node:assert/strict";
import { formatDecimal, makeRational, parseDecimal } from "../src/index.ts";

test("parseDecimal: exact decimal parsing without Number", () => {
  assert.deepEqual(parseDecimal("3.14"), { num: 157n, den: 50n });
  assert.deepEqual(parseDecimal("-0.5"), { num: -1n, den: 2n });
  assert.deepEqual(parseDecimal(".5"), { num: 1n, den: 2n });
  assert.deepEqual(parseDecimal("5."), { num: 5n, den: 1n });
  assert.deepEqual(parseDecimal("1e-3"), { num: 1n, den: 1000n });
  assert.deepEqual(parseDecimal("2.5e2"), { num: 250n, den: 1n });
  assert.deepEqual(parseDecimal("-1.25e-2"), { num: -1n, den: 80n });
});

test("parseDecimal: 40-digit decimal stays exact (BigInt, no Number)", () => {
  const r = parseDecimal("3.1415926535897932384626433832795028841971");
  assert.equal(r.num, 31415926535897932384626433832795028841971n);
  assert.equal(r.den, 10n ** 40n);
});

test("parseDecimal: p/q fractions and normalization", () => {
  assert.deepEqual(parseDecimal("22/7"), { num: 22n, den: 7n });
  assert.deepEqual(parseDecimal("-3/-6"), { num: 1n, den: 2n });
  assert.deepEqual(parseDecimal("4/-8"), { num: -1n, den: 2n });
  assert.deepEqual(parseDecimal("0/5"), { num: 0n, den: 1n });
});

test("parseDecimal: rejects garbage", () => {
  assert.throws(() => parseDecimal("abc"), SyntaxError);
  assert.throws(() => parseDecimal("1.2.3"), SyntaxError);
  assert.throws(() => parseDecimal(""), SyntaxError);
  assert.throws(() => parseDecimal("1/0"), RangeError);
});

test("makeRational: normalizes sign and reduces", () => {
  assert.deepEqual(makeRational(6n, -9n), { num: -2n, den: 3n });
  assert.deepEqual(makeRational(0n, 7n), { num: 0n, den: 1n });
});

test("formatDecimal: explicit rounding modes", () => {
  const eighth = makeRational(1n, 8n);
  assert.equal(formatDecimal(eighth, 2, "half-up"), "0.13");
  assert.equal(formatDecimal(eighth, 2, "half-even"), "0.12");
  assert.equal(formatDecimal(eighth, 2, "truncate"), "0.12");
  const negEighth = makeRational(-1n, 8n);
  assert.equal(formatDecimal(negEighth, 2, "half-up"), "-0.13");
  assert.equal(formatDecimal(negEighth, 2, "half-even"), "-0.12");
  assert.equal(formatDecimal(negEighth, 2, "truncate"), "-0.12");
  assert.equal(formatDecimal(makeRational(2n, 3n), 4, "half-up"), "0.6667");
  assert.equal(formatDecimal(makeRational(355n, 113n), 6, "half-up"), "3.141593");
  assert.equal(formatDecimal(makeRational(1n, 2n), 0, "half-up"), "1");
  assert.equal(formatDecimal(makeRational(1n, 2n), 0, "half-even"), "0");
  assert.equal(formatDecimal(makeRational(3n, 2n), 0, "half-even"), "2");
});
