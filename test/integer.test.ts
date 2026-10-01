import { test } from "node:test";
import assert from "node:assert/strict";
import { chunkSizeFor, digitsToInt, intToDigits, InvalidDigitError, type Workload } from "../src/integer.ts";

// Deterministic PRNG so failures reproduce.
function mulberry32(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("matches native BigInt.toString for bases 2, 8, 10, 16, 36", () => {
  const rnd = mulberry32(42);
  for (const base of [2, 8, 10, 16, 36]) {
    for (let i = 0; i < 50; i++) {
      const bits = 1 + Math.floor(rnd() * 512);
      let v = 0n;
      for (let b = 0; b < bits; b++) v = (v << 1n) | BigInt(rnd() < 0.5 ? 1 : 0);
      assert.equal(intToDigits(v, base, "lower"), v.toString(base), `base ${base} value ${v}`);
    }
  }
});

test("round-trips across every base 2..62", () => {
  const rnd = mulberry32(7);
  for (let base = 2; base <= 62; base++) {
    const charset = base <= 36 ? "lower" : "base62";
    for (let i = 0; i < 10; i++) {
      let v = 0n;
      const bits = 1 + Math.floor(rnd() * 256);
      for (let b = 0; b < bits; b++) v = (v << 1n) | BigInt(rnd() < 0.5 ? 1 : 0);
      const s = intToDigits(v, base, charset);
      assert.equal(digitsToInt(s, base, charset), v, `base ${base}`);
    }
  }
});

test("zero and one", () => {
  assert.equal(intToDigits(0n, 10, "lower"), "0");
  assert.equal(intToDigits(1n, 2, "lower"), "1");
  assert.equal(digitsToInt("0", 10, "lower"), 0n);
});

test("chunking keeps bignum divisions far below the digit count", () => {
  // A 1000-decimal-digit number: naive per-digit division would cost ~1000
  // full-length divisions; chunked conversion must use a small fraction.
  const digits = "9".repeat(1000);
  const w1: Workload = { divisions: 0, multiplyAdds: 0 };
  const v = digitsToInt(digits, 10, "lower", w1);
  const w2: Workload = { divisions: 0, multiplyAdds: 0 };
  const hex = intToDigits(v, 16, "lower", w2);
  assert.equal(hex, v.toString(16));
  assert.ok(w1.multiplyAdds <= 1000 / 10, `parse multiply-adds: ${w1.multiplyAdds}`);
  assert.ok(w2.divisions <= 1000 / 10, `emit divisions: ${w2.divisions}`);
  // chunk size sanity: 10^15 <= 2^53 < 10^16
  assert.equal(chunkSizeFor(10), 15);
  assert.equal(chunkSizeFor(2), 53);
});

test("invalid digit reports exact position", () => {
  try {
    digitsToInt("12a4", 10, "lower");
    assert.fail("should throw");
  } catch (e) {
    const err = e as InvalidDigitError;
    assert.ok(err instanceof InvalidDigitError);
    assert.equal(err.position, 2);
    assert.equal(err.char, "a");
    assert.equal(err.base, 10);
  }
});

test("invalid digit position respects offset (fraction part of a numeral)", () => {
  try {
    digitsToInt("0x", 10, "lower", undefined, 4);
    assert.fail("should throw");
  } catch (e) {
    assert.equal((e as InvalidDigitError).position, 5);
  }
});

test("case sensitivity: wrong case is an invalid digit", () => {
  assert.throws(() => digitsToInt("A", 16, "lower"), InvalidDigitError);
  assert.equal(digitsToInt("A", 16, "upper"), 10n);
});
