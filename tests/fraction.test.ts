import { test } from "node:test";
import assert from "node:assert/strict";
import {
  add,
  bitLength,
  cmp,
  div,
  eq,
  frac,
  gcd,
  isInt,
  isZero,
  mul,
  neg,
  reciprocal,
  roundToDecimal,
  sign,
  sub,
  toFractionString,
  type Fraction,
} from "../src/fraction.ts";

test("canonical form: sign moves to numerator, coprime, zero is 0/1", () => {
  assert.deepEqual(frac(2, 4), { n: 1n, d: 2n });
  assert.deepEqual(frac(1, -6), { n: -1n, d: 6n });
  assert.deepEqual(frac(-1, -6), { n: 1n, d: 6n });
  assert.deepEqual(frac(0, -9), { n: 0n, d: 1n });
  assert.deepEqual(frac(-0n, 7n), { n: 0n, d: 1n });
  for (const d of [3n, -5n, 12n]) {
    const z = frac(0n, d);
    assert.equal(z.d, 1n);
    assert.equal(z.n, 0n);
  }
});

test("zero denominator and non-integer input are rejected", () => {
  assert.throws(() => frac(1, 0), /Zero denominator/);
  assert.throws(() => frac(1.5), /integer/);
  assert.throws(() => div(frac(1), frac(0)), /Division by zero/);
  assert.throws(() => reciprocal(frac(0)), /Reciprocal of zero/);
});

test("four operations over BigInt", () => {
  const sixth = frac(1n, 6n);
  const seventh = frac(1n, 7n);
  assert.deepEqual(add(sixth, seventh), frac(13n, 42n));
  assert.deepEqual(sub(seventh, sixth), frac(-1n, 42n));
  assert.deepEqual(mul(sixth, seventh), frac(1n, 42n));
  assert.deepEqual(div(sixth, seventh), frac(7n, 6n));
  assert.deepEqual(neg(sixth), frac(-1n, 6n));
  assert.deepEqual(reciprocal(sixth), frac(6n, 1n));
});

test("operations normalize signs after subtraction through zero", () => {
  const a = frac(1n, 3n);
  assert.ok(isZero(sub(a, a)));
  assert.deepEqual(sub(a, a), { n: 0n, d: 1n });
  assert.deepEqual(mul(frac(-2n, 3n), frac(-3n, 2n)), frac(1n));
});

test("cross multiplication comparison, including negatives", () => {
  assert.equal(cmp(frac(1n, 7n), frac(1n, 6n)), -1);
  assert.equal(cmp(frac(-1n, 6n), frac(-1n, 7n)), -1);
  assert.equal(cmp(frac(2n, 4n), frac(1n, 2n)), 0);
  assert.ok(eq(frac(2n, 4n), frac(1n, 2n)));
  assert.equal(sign(frac(-3n, -9n)), 1);
  assert.equal(sign(frac(0n, 2n)), 0);
  assert.equal(isInt(frac(8n, 2n)), true);
  assert.equal(isInt(frac(9n, 2n)), false);
});

test("huge integers never round-trip through Number", () => {
  const big1 = 10n ** 100n + 1n;
  const big2 = 10n ** 100n - 1n;
  const f = frac(big1, big2);
  assert.equal(f.n, big1);
  assert.equal(f.d, big2);
  assert.equal(cmp(f, frac(1n)), 1);
  const sum = add(f, frac(1n));
  assert.equal(sum.n, big1 + big2);
  assert.equal(bitLength(big1), 333);
  assert.equal(bitLength(0n), 0);
  assert.equal(toFractionString(frac(3n)), "3");
});

test("gcd", () => {
  assert.equal(gcd(0n, 5n), 5n);
  assert.equal(gcd(-12n, 8n), 4n);
});

test("roundToDecimal: explicit modes, exact Fraction results", () => {
  const cases: Array<[Fraction, number, "floor" | "ceil" | "trunc" | "half-up", string]> = [
    [frac(1n, 6n), 2, "half-up", "17/100"],
    [frac(1n, 6n), 4, "half-up", "1667/10000"],
    [frac(-1n, 6n), 2, "half-up", "-17/100"],
    [frac(5n, 2n), 1, "half-up", "5/2"],
    [frac(1n, 3n), 2, "trunc", "33/100"],
    [frac(-1n, 3n), 2, "trunc", "-33/100"],
    [frac(1n, 3n), 2, "floor", "33/100"],
    [frac(-1n, 3n), 2, "floor", "-17/50"],
    [frac(1n, 3n), 2, "ceil", "17/50"],
    [frac(-1n, 3n), 2, "ceil", "-33/100"],
    [frac(5n, 10n), 3, "trunc", "1/2"],
    [frac(1n, 20n), 0, "half-up", "0"],
    [frac(15n, 10n), 0, "half-up", "2"],
    [frac(-15n, 10n), 0, "half-up", "-2"],
    [frac(25n, 100n), 1, "half-up", "3/10"],
  ];
  for (const [f, digits, mode, expected] of cases) {
    assert.equal(toFractionString(roundToDecimal(f, digits, mode)), expected, `${toFractionString(f)} ${digits} ${mode}`);
  }
  assert.throws(() => roundToDecimal(frac(1n), -1), /non-negative/);
});
