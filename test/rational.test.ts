import { test } from "node:test";
import assert from "node:assert/strict";
import {
  rat, fromString, toString, add, sub, mul, div, neg, abs, cmp,
  floorRat, ceilRat,
} from "../src/rational.ts";

test("规范化：约分、分母恒正", () => {
  assert.deepEqual(rat(2n, 4n), { n: 1n, d: 2n });
  assert.deepEqual(rat(3n, -7n), { n: -3n, d: 7n });
  assert.deepEqual(rat(-3n, -7n), { n: 3n, d: 7n });
});

test("负零策略：零永远规范为 0/1，无负零", () => {
  assert.deepEqual(rat(0n, -5n), { n: 0n, d: 1n });
  assert.deepEqual(neg(rat(0n)), { n: 0n, d: 1n });
  assert.equal(toString(fromString("-0")), "0");
  assert.equal(toString(fromString("-0.000")), "0");
  assert.equal(toString(mul(fromString("-3.5"), rat(0n))), "0");
});

test("大数不经过 Number：精确运算", () => {
  const big = rat(2n ** 200n);
  const third = rat(1n, 3n);
  const r = mul(big, third);
  assert.equal(r.n, 2n ** 200n);
  assert.equal(r.d, 3n);
  assert.equal(toString(add(rat(10n ** 30n), rat(1n))), (10n ** 30n + 1n).toString());
});

test("十进制解析是精确的（0.1 = 1/10）", () => {
  const a = fromString("0.1");
  assert.deepEqual(a, { n: 1n, d: 10n });
  assert.equal(toString(a), "0.1");
  assert.equal(toString(fromString("-2.75")), "-2.75");
  assert.equal(toString(fromString("22/7")), "22/7"); // 非有限小数保持分数
});

test("四则运算与比较", () => {
  assert.equal(toString(add(fromString("0.1"), fromString("0.2"))), "0.3");
  assert.equal(toString(sub(rat(1n), rat(1n, 3n))), "2/3");
  assert.equal(toString(div(rat(1n, 2n), rat(3n, 4n))), "2/3");
  assert.equal(toString(abs(rat(-5n, 2n))), "2.5");
  assert.ok(cmp(rat(1n, 3n), rat(1n, 2n)) < 0);
});

test("floor/ceil 对负数正确", () => {
  assert.equal(floorRat(rat(-7n, 2n)), -4n);
  assert.equal(ceilRat(rat(-7n, 2n)), -3n);
  assert.equal(floorRat(rat(7n, 2n)), 3n);
  assert.equal(ceilRat(rat(7n, 2n)), 4n);
});
