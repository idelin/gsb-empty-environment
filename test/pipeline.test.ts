import { test } from "node:test";
import assert from "node:assert/strict";
import { rat, fromString, toString as rs, abs, sub, cmp } from "../src/rational.ts";
import { affine, quantizeOp } from "../src/ops.ts";
import type { Op } from "../src/ops.ts";
import {
  runPipeline, compareModes, pipelineErrorBound,
} from "../src/pipeline.ts";

const ops: Op[] = [
  affine(fromString("1.5"), fromString("-0.25"), "scale"),
  quantizeOp([{ lo: null, hi: null, step: fromString("0.5"), rule: "half-up" }], "round"),
  affine(rat(2n), rat(1n), "boost"),
];
const finalQuant = { step: rat(1n), rule: "half-even" } as const;

test("逐步审计：每步记录输入/输出/损失/上界", () => {
  const r = runPipeline(ops, rat(2n), "stepwise");
  assert.equal(r.steps.length, 3);
  assert.equal(rs(r.steps[1].input), "2.75");
  assert.equal(rs(r.steps[1].output), "3");
  assert.equal(rs(r.steps[1].loss), "0.25");
  assert.equal(rs(r.steps[1].maxLoss), "0.25");
  assert.equal(rs(r.output), "7");
});

test("延迟模式：中间不舍入，末端统一舍入一次", () => {
  const r = runPipeline(ops, rat(2n), "deferred", finalQuant);
  assert.ok(r.steps[1].deferred);
  assert.equal(rs(r.steps[1].input), "2.75");
  assert.equal(rs(r.steps[1].output), "2.75"); // 原样通过
  const last = r.steps[r.steps.length - 1];
  assert.equal(last.kind, "final-round");
  assert.equal(rs(last.input), "6.5");
  assert.equal(rs(last.output), "6"); // half-even: 6.5 -> 6
});

test("两种模式结果可能不同且都被保留", () => {
  const c = compareModes(ops, rat(2n), finalQuant);
  assert.ok(c.differs);
  assert.equal(rs(c.stepwise.output), "7");
  assert.equal(rs(c.deferred.output), "6");
  assert.match(c.reason, /首个分歧点/);
  assert.match(c.reason, /round/);
  assert.match(c.reason, /放大系数 2/);
  const same = compareModes(ops, rat(1n), finalQuant);
  assert.ok(!same.differs);
  assert.match(same.reason, /一致/);
});

test("累计误差界覆盖真实损失（网格上逐一验证）", () => {
  assert.equal(rs(pipelineErrorBound(ops)), "0.5"); // 0.25 * |2| = 0.5
  for (let i = -20n; i <= 20n; i++) {
    const r = runPipeline(ops, rat(i), "stepwise");
    const deviation = abs(sub(r.output, r.exactValue));
    assert.ok(
      cmp(deviation, r.errorBound) <= 0,
      `x=${i}: 偏差 ${rs(deviation)} 超过界 ${rs(r.errorBound)}`,
    );
  }
});

test("负零策略：零输出规范为 0/1", () => {
  const z = runPipeline(
    [quantizeOp([{ lo: null, hi: null, step: rat(1n), rule: "half-even" }])],
    fromString("-0.4"),
    "stepwise",
  );
  assert.deepEqual(z.output, { n: 0n, d: 1n });
  assert.equal(rs(z.output), "0");
});

test("大数输入不经过 Number", () => {
  const big = rat(2n ** 150n);
  const r = runPipeline(
    [affine(rat(3n), rat(1n)), quantizeOp([{ lo: null, hi: null, step: rat(1n), rule: "half-up" }])],
    big,
    "stepwise",
  );
  assert.equal(r.output.n, 3n * 2n ** 150n + 1n);
});
