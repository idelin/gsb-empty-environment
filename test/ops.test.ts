import { test } from "node:test";
import assert from "node:assert/strict";
import { rat, fromString, toString as rs } from "../src/rational.ts";
import { interval, intervalToString, contains } from "../src/interval.ts";
import {
  affine, quantizeOp, applyOp, preimageOp, pipelinePreimage, findSegment,
} from "../src/ops.ts";
import type { Op } from "../src/ops.ts";

test("仿射前向与原像（正缩放保持开闭，负缩放翻转）", () => {
  const up = affine(rat(2n), rat(1n));
  assert.equal(rs(applyOp(up, rat(3n))), "7");
  const [pre] = preimageOp(up, interval(rat(3n), true, rat(7n), false));
  assert.equal(intervalToString(pre), "[1, 3)");
  const down = affine(rat(-2n), rat(0n));
  const [pre2] = preimageOp(down, interval(rat(0n), true, rat(4n), false));
  assert.equal(intervalToString(pre2), "(-2, 0]"); // 端点随值翻转
});

test("量化原像保留平台与开闭端点", () => {
  const q = quantizeOp([{ lo: null, hi: null, step: rat(1n), rule: "half-up" }]);
  const pres = preimageOp(q, interval(rat(1n), true, rat(3n), true)); // 输出 1,2,3
  assert.deepEqual(pres.map(intervalToString), ["[0.5, 1.5)", "[1.5, 2.5)", "[2.5, 3.5)"]);
});

test("half-even 平台端点随奇偶交替", () => {
  const q = quantizeOp([{ lo: null, hi: null, step: rat(1n), rule: "half-even" }]);
  const pres = preimageOp(q, interval(rat(1n), true, rat(2n), true));
  assert.deepEqual(pres.map(intervalToString), ["(0.5, 1.5)", "[1.5, 2.5]"]);
});

test("分段量化：边界归属与分段原像", () => {
  const q = quantizeOp([
    { lo: null, hi: rat(0n), step: rat(2n), rule: "floor" },
    { lo: rat(0n), hi: null, step: rat(1n), rule: "half-even" },
  ]);
  assert.equal(findSegment(q, rat(0n)).step.n, 1n); // 0 属于第二段
  assert.equal(rs(applyOp(q, rat(-1n))), "-2"); // floor 到 2 的倍数
  assert.equal(rs(applyOp(q, fromString("0.5"))), "0"); // half-even
  const pres = preimageOp(q, interval(rat(0n), true, rat(0n), true)); // 输出 {0}
  // 负段 floor 输出 0 的平台是 [0,2)，与定义域 (-inf,0) 不交 → 不产生；
  // 正段 half-even 零级平台 [-0.5,0.5] ∩ [0,+inf) = [0, 0.5]
  assert.deepEqual(pres.map(intervalToString), ["[0, 0.5]"]);
  // 输出 {-2} 只能来自负段：floor 平台 [-2, 0)
  const pres2 = preimageOp(q, interval(rat(-2n), true, rat(-2n), true));
  assert.deepEqual(pres2.map(intervalToString), ["[-2, 0)"]);
});

test("流水线原像：复合后平台不被合并", () => {
  const ops: Op[] = [
    affine(fromString("1.5"), fromString("-0.25")),
    quantizeOp([{ lo: null, hi: null, step: fromString("0.5"), rule: "half-up" }]),
    affine(rat(2n), rat(1n)),
  ];
  const pres = pipelinePreimage(ops, interval(rat(10n), true, rat(10n), true));
  assert.deepEqual(pres.map(intervalToString), ["[3, 10/3)"]);
  // 区间内每个有理点都映射到 10，端点 10/3 不映射到 10
  assert.ok(pres.some((iv) => contains(iv, rat(3n))));
  assert.ok(!pres.some((iv) => contains(iv, rat(10n, 3n))));
});
