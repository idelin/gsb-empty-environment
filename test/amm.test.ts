import { test } from "node:test";
import assert from "node:assert/strict";
import { ConstantProductPool } from "../src/amm.ts";
import { Rat } from "../src/rational.ts";

test("零手续费输出等于整数公式参考值", () => {
  const pool = new ConstantProductPool(1000n, 1000n, 0n, 1n);
  assert.equal(pool.getAmountOut(100n, "zeroForOne"), 90n); // floor(1000*100/1100)
});

test("手续费舍入：输出等于精确有理 floor 且为最大整数", () => {
  const x = 1_000_000n;
  const y = 2_000_000n;
  const feeNum = 3n;
  const feeDen = 1000n;
  const amountIn = 12_345n;
  const pool = new ConstantProductPool(x, y, feeNum, feeDen);

  const out = pool.getAmountOut(amountIn, "zeroForOne");

  // 精确有理参考：eff = in*(feeDen-feeNum)/feeDen，out = floor(y*eff/(x+eff))
  const eff = Rat.of(amountIn * (feeDen - feeNum), feeDen);
  const expected = Rat.of(y)
    .mul(eff)
    .div(Rat.of(x).add(eff))
    .floor();
  assert.equal(out, expected);

  // 不变式（乘以 feeDen 化为整数）：(x*feeDen + in*(feeDen-feeNum)) * (y-out) >= x*y*feeDen
  const effNum = amountIn * (feeDen - feeNum);
  const lhs = (x * feeDen + effNum) * (y - out);
  const rhs = x * y * feeDen;
  assert.ok(lhs >= rhs, "x*y 不减");
  // 最大性：out+1 必破坏不变式
  const lhsNext = (x * feeDen + effNum) * (y - (out + 1n));
  assert.ok(lhsNext < rhs, "out 已是最大整数输出");
});

test("交换后储备按最大整数输出更新且不变式不减", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 3n, 1000n);
  const k0 = pool.invariant();
  const out = pool.swap(50_000n, "oneForZero", 0n);
  assert.equal(pool.y, 1_050_000n);
  assert.equal(pool.x, 1_000_000n - out);
  assert.ok(pool.invariant() >= k0);
});

test("非法输入与储备约束", () => {
  assert.throws(() => new ConstantProductPool(0n, 1n, 0n, 1n), RangeError);
  assert.throws(() => new ConstantProductPool(1n, 1n, 1n, 1n), RangeError);
  const pool = new ConstantProductPool(100n, 100n, 0n, 1n);
  assert.throws(() => pool.getAmountOut(0n, "zeroForOne"), RangeError);
  // 输出恒小于储备，无法抽干
  const huge = pool.getAmountOut(1n << 120n, "zeroForOne");
  assert.ok(huge < 100n);
});
