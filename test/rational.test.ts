import { test } from "node:test";
import assert from "node:assert/strict";
import {
  add,
  ceil,
  cmp,
  floor,
  make,
  parseRational,
  sub,
  toDecimal,
  toString,
} from "../src/rational.ts";

test("parses exact decimals without Number intermediates", () => {
  const x = parseRational("3.14159265358979323846264338327950288419716939937510582097494459");
  assert.equal(x.den, 10n ** 62n); // 62 fractional digits, kept exactly
  assert.equal(x.num, 314159265358979323846264338327950288419716939937510582097494459n);
  assert.equal(toString(parseRational("22/7")), "22/7");
  assert.equal(toString(parseRational("-0.5")), "-1/2");
  assert.equal(toString(parseRational("+4.500")), "9/2"); // normalized
  assert.equal(toString(parseRational("-3")), "-3");
  assert.equal(toString(parseRational("0.000")), "0");
});

test("normalizes sign and lowest terms", () => {
  assert.deepEqual(make(6n, -8n), { num: -3n, den: 4n });
  assert.deepEqual(make(0n, -99n), { num: 0n, den: 1n });
  assert.throws(() => make(1n, 0n));
});

test("floor and ceil are true floor/ceil for negatives", () => {
  assert.equal(floor(parseRational("-3.5")), -4n);
  assert.equal(ceil(parseRational("-3.5")), -3n);
  assert.equal(floor(parseRational("3.5")), 3n);
  assert.equal(ceil(parseRational("3.5")), 4n);
  assert.equal(floor(parseRational("-4")), -4n);
  assert.equal(ceil(parseRational("-4")), -4n);
});

test("arithmetic and comparison are exact", () => {
  const a = parseRational("1/3");
  const b = parseRational("1/6");
  assert.equal(toString(add(a, b)), "1/2");
  assert.equal(toString(sub(a, b)), "1/6");
  assert.equal(cmp(parseRational("-2/3"), parseRational("-5/8")), -1);
  assert.equal(cmp(parseRational("0.1"), parseRational("1/10")), 0);
});

test("exact decimal expansion is truncated, never rounded", () => {
  assert.equal(toDecimal(parseRational("1/3"), 5), "0.33333");
  assert.equal(toDecimal(parseRational("-2/3"), 3), "-0.666");
  assert.equal(toDecimal(parseRational("1/7"), 0), "0");
});

test("rejects malformed input", () => {
  assert.throws(() => parseRational("abc"));
  assert.throws(() => parseRational("1.2.3"));
  assert.throws(() => parseRational("1/2/3"));
  assert.throws(() => parseRational("0.5/2"));
});
