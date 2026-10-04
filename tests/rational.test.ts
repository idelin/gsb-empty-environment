import { test } from "node:test";
import assert from "node:assert/strict";
import {
  rat,
  add,
  sub,
  mul,
  div,
  neg,
  cmp,
  parseRat,
  formatRat,
  ZERO,
} from "../src/rational.ts";

test("构造时自动约分且分母为正", () => {
  assert.deepEqual(rat(2n, 4n), { num: 1n, den: 2n });
  assert.deepEqual(rat(1n, -2n), { num: -1n, den: 2n });
  assert.deepEqual(rat(-3n, -9n), { num: 1n, den: 3n });
  assert.deepEqual(rat(10n, 5n), { num: 2n, den: 1n });
  assert.equal(rat(0n, 7n), ZERO);
});

test("零分母抛错", () => {
  assert.throws(() => rat(1n, 0n));
  assert.throws(() => div(rat(1n), ZERO));
});

test("四则运算精确", () => {
  const a = parseRat("1/3");
  const b = parseRat("1/6");
  assert.equal(formatRat(add(a, b)), "1/2");
  assert.equal(formatRat(sub(a, b)), "1/6");
  assert.equal(formatRat(mul(a, b)), "1/18");
  assert.equal(formatRat(div(a, b)), "2");
  assert.equal(formatRat(neg(a)), "-1/3");
  // 浮点会出错的经典例子：1/3 + 1/3 + 1/3 精确等于 1
  assert.equal(formatRat(add(add(a, a), a)), "1");
});

test("比较与解析/格式化往返", () => {
  assert.equal(cmp(parseRat("2/3"), parseRat("3/4")), -1);
  assert.equal(cmp(parseRat("-1/2"), parseRat("-2/4")), 0);
  assert.equal(cmp(parseRat("5"), parseRat("4/1")), 1);
  for (const s of ["0", "3", "-7", "22/7", "-3/4", "100000000000000000000/3"]) {
    const once = formatRat(parseRat(s));
    assert.equal(formatRat(parseRat(once)), once); // 解析/格式化幂等
  }
  assert.equal(formatRat(parseRat("6/4")), "3/2"); // 输出已约分
});

test("大整数不溢出", () => {
  const big = parseRat("123456789012345678901234567890/98765432109876543210");
  const one = parseRat("1");
  const r = sub(mul(big, div(one, big)), one);
  assert.equal(formatRat(r), "0");
});
