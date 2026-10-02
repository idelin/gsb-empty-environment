import { test } from "node:test";
import assert from "node:assert/strict";
import { rat, add, sub, mul, div, cmp, roundToBigInt, toString, toDecimalString } from "../src/rational.ts";

test("reduces and normalises sign", () => {
  assert.deepEqual(rat(30000n, 1001n), { num: 30000n, den: 1001n });
  assert.deepEqual(rat(6n, -8n), { num: -3n, den: 4n });
  assert.deepEqual(rat(-6n, -8n), { num: 3n, den: 4n });
  assert.throws(() => rat(1n, 0n), RangeError);
});

test("arithmetic stays exact beyond 2^53", () => {
  const big = 2n ** 60n + 1n; // not representable as a Number
  const r = rat(big, 3n);
  assert.equal(add(r, r).num, 2n * big);
  assert.equal(mul(rat(big, 1n), rat(1n, big)).num, 1n);
  assert.equal(sub(rat(1n, 3n), rat(1n, 6n)).num, 1n);
  assert.equal(cmp(div(rat(1n, 2n), rat(1n, 4n)), rat(2n)), 0);
});

test("explicit rounding modes", () => {
  const cases: Array<[string, bigint, bigint, Record<string, bigint>]> = [
    ["positive fraction", 7n, 2n, { floor: 3n, ceil: 4n, trunc: 3n, "half-up": 4n, "half-even": 4n }],
    ["negative fraction", -7n, 2n, { floor: -4n, ceil: -3n, trunc: -3n, "half-up": -4n, "half-even": -4n }],
    ["half to even down", 5n, 2n, { floor: 2n, ceil: 3n, trunc: 2n, "half-up": 3n, "half-even": 2n }],
    ["half to even up", 3n, 2n, { floor: 1n, ceil: 2n, trunc: 1n, "half-up": 2n, "half-even": 2n }],
    ["below half", 5n, 3n, { floor: 1n, ceil: 2n, trunc: 1n, "half-up": 2n, "half-even": 2n }],
    ["exact", 8n, 4n, { floor: 2n, ceil: 2n, trunc: 2n, "half-up": 2n, "half-even": 2n }],
  ];
  for (const [name, num, den, expected] of cases) {
    for (const [mode, want] of Object.entries(expected)) {
      assert.equal(roundToBigInt(rat(num, den), mode as never), want, `${name} ${mode}`);
    }
  }
});

test("rendering", () => {
  assert.equal(toString(rat(30000n, 1001n)), "30000/1001");
  assert.equal(toString(rat(4n, 2n)), "2");
  assert.equal(toDecimalString(rat(1n, 3n), 6, "trunc"), "0.333333");
  assert.equal(toDecimalString(rat(-1n, 120000n), 9, "half-up"), "-0.000008333");
});
