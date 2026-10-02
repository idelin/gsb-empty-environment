import { test } from "node:test";
import assert from "node:assert/strict";
import { rat, fromString, toString } from "../src/rational.ts";
import {
  roundRat, quantize, maxQuantizeLoss, plateauForLevel,
} from "../src/rounding.ts";

const r = (s: string) => roundRat(fromString(s), "half-up");

test("各舍入规则的平局行为显式", () => {
  assert.equal(roundRat(fromString("2.5"), "half-up"), 3n); // 平局向 +inf
  assert.equal(roundRat(fromString("-2.5"), "half-up"), -2n);
  assert.equal(roundRat(fromString("2.5"), "half-even"), 2n); // 平局向偶数
  assert.equal(roundRat(fromString("3.5"), "half-even"), 4n);
  assert.equal(roundRat(fromString("-2.5"), "half-even"), -2n);
  assert.equal(roundRat(fromString("2.5"), "half-away"), 3n); // 平局远离零
  assert.equal(roundRat(fromString("-2.5"), "half-away"), -3n);
  assert.equal(roundRat(fromString("2.5"), "floor"), 2n);
  assert.equal(roundRat(fromString("-2.5"), "floor"), -3n);
  assert.equal(roundRat(fromString("2.5"), "ceil"), 3n);
  assert.equal(roundRat(fromString("-2.5"), "ceil"), -2n);
  assert.equal(roundRat(fromString("2.5"), "trunc"), 2n); // 向零截断
  assert.equal(roundRat(fromString("-2.5"), "trunc"), -2n);
  assert.equal(r("2.49"), 2n);
});

test("舍入零结果是 0n，无负零", () => {
  assert.equal(roundRat(fromString("-0.4"), "half-up"), 0n);
  assert.equal(roundRat(fromString("-0.5"), "half-even"), 0n);
  assert.equal(roundRat(fromString("-0.9"), "trunc"), 0n);
});

test("quantize 吸附到步长倍数", () => {
  assert.equal(toString(quantize(fromString("2.25"), fromString("0.5"), "half-even")), "2");
  assert.equal(toString(quantize(fromString("2.75"), fromString("0.5"), "half-even")), "3");
  assert.equal(toString(quantize(fromString("-1.3"), fromString("0.25"), "floor")), "-1.5");
});

test("最大损失界：half-* 为 step/2，有向规则为 step", () => {
  assert.equal(toString(maxQuantizeLoss(rat(2n), "half-up")), "1");
  assert.equal(toString(maxQuantizeLoss(rat(2n), "floor")), "2");
  assert.equal(toString(maxQuantizeLoss(rat(2n), "trunc")), "2");
});

test("舍入平台端点开闭与平局归属一致", () => {
  const step = rat(1n);
  // half-up: [k-1/2, k+1/2)
  let p = plateauForLevel(2n, step, "half-up");
  assert.ok(p.loClosed && !p.hiClosed);
  assert.equal(toString(p.lo), "1.5");
  // half-even: 偶数级两端闭，奇数级两端开
  p = plateauForLevel(2n, step, "half-even");
  assert.ok(p.loClosed && p.hiClosed);
  p = plateauForLevel(3n, step, "half-even");
  assert.ok(!p.loClosed && !p.hiClosed);
  // half-away: 零级两端开
  p = plateauForLevel(0n, step, "half-away");
  assert.ok(!p.loClosed && !p.hiClosed);
  // floor: [k, k+1)
  p = plateauForLevel(2n, step, "floor");
  assert.ok(p.loClosed && !p.hiClosed);
  assert.equal(toString(p.lo), "2");
  // trunc: 零级 (-1, 1)
  p = plateauForLevel(0n, step, "trunc");
  assert.ok(!p.loClosed && !p.hiClosed);
  assert.equal(toString(p.hi), "1");
});

test("平台与 roundRat 在平局点上一致（抽样核验）", () => {
  const step = fromString("0.5");
  const rules = ["floor", "ceil", "trunc", "half-up", "half-away", "half-even"] as const;
  for (const rule of rules)
    for (let k = -6n; k <= 6n; k++) {
      const p = plateauForLevel(k, step, rule);
      // 端点处的归属必须与 quantize 的实际行为一致
      const loMapsHere =
        quantize(p.lo, step, rule).n * step.d === k * step.n * (p.lo.d === 1n ? 1n : 1n);
      assert.equal(
        p.loClosed,
        toString(quantize(p.lo, step, rule)) === toString({ n: k * step.n, d: step.d }),
        `${rule} level ${k} lo`,
      );
      assert.equal(
        p.hiClosed,
        toString(quantize(p.hi, step, rule)) === toString({ n: k * step.n, d: step.d }),
        `${rule} level ${k} hi`,
      );
      void loMapsHere;
    }
});
