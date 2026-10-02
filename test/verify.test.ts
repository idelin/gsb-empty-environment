import { test } from "node:test";
import assert from "node:assert/strict";
import { rat, fromString, toString as rs, cmp } from "../src/rational.ts";
import { affine, quantizeOp } from "../src/ops.ts";
import type { Op } from "../src/ops.ts";
import { runPipeline } from "../src/pipeline.ts";
import { exhaustiveVerify } from "../src/verify.ts";

const ops: Op[] = [
  affine(fromString("1.5"), fromString("-0.25"), "scale"),
  quantizeOp([{ lo: null, hi: null, step: fromString("0.5"), rule: "half-up" }], "round"),
  affine(rat(2n), rat(1n), "boost"),
  quantizeOp(
    [
      { lo: null, hi: rat(0n), step: rat(2n), rule: "floor" },
      { lo: rat(0n), hi: null, step: rat(1n), rule: "half-even" },
    ],
    "piecewise",
  ),
];
const finalQuant = { step: rat(1n), rule: "half-even" } as const;

test("穷举核验：正逆映射一致、误差界覆盖、无失败", () => {
  const report = exhaustiveVerify(ops, -8n, 8n, finalQuant);
  assert.equal(report.checked, 17);
  assert.deepEqual(report.failures, []);
  assert.ok(cmp(report.maxObservedDeviation, report.errorBound) <= 0);
});

test("自动找到最小差异输入，且与暴力枚举一致", () => {
  const report = exhaustiveVerify(ops, -8n, 8n, finalQuant);
  assert.ok(report.minDiff !== null);
  // 暴力复核：最小 |x| 的差异输入
  let brute: bigint | null = null;
  for (let i = -8n; i <= 8n; i++) {
    const sw = runPipeline(ops, rat(i), "stepwise").output;
    const df = runPipeline(ops, rat(i), "deferred", finalQuant).output;
    if (cmp(sw, df) !== 0 && (brute === null || (i < 0n ? -i : i) < (brute < 0n ? -brute : brute)))
      brute = i;
  }
  assert.ok(brute !== null);
  assert.equal(report.minDiff!.input.n, brute);
  assert.equal(rs(report.minDiff!.input), "0");
  assert.equal(rs(report.minDiff!.stepwise), "1");
  assert.equal(rs(report.minDiff!.deferred), "0");
  assert.match(report.minDiff!.reason, /首个分歧点/);
});

test("无差异流水线的 minDiff 为 null", () => {
  const tame: Op[] = [
    affine(rat(2n), rat(0n)),
    quantizeOp([{ lo: null, hi: null, step: rat(2n), rule: "half-up" }]),
  ];
  // 仿射输出恒为偶数，量化步长 2：逐步无损失，与延迟一致
  const report = exhaustiveVerify(tame, -5n, 5n, { step: rat(2n), rule: "half-up" });
  assert.deepEqual(report.failures, []);
  assert.equal(report.minDiff, null);
});
