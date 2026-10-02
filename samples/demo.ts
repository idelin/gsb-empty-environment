/**
 * 运行样例：node samples/demo.ts
 *
 * 演示：
 *  1. 逐步执行 + 每步损失审计（stepwise 模式）
 *  2. 延迟统一舍入对照（deferred 模式）与差异原因
 *  3. 输出区间的完整输入原像（保留舍入平台与开闭端点）
 *  4. 复合简化（不移动舍入步骤）
 *  5. 小缩放整数网格穷举核验 + 最小差异输入
 */
import { rat, fromString, toString as rs } from "../src/rational.ts";
import { affine, quantizeOp, pipelinePreimage, describeOp } from "../src/ops.ts";
import { interval, intervalToString } from "../src/interval.ts";
import { runPipeline, compareModes, pipelineErrorBound } from "../src/pipeline.ts";
import { simplifyOps } from "../src/simplify.ts";
import { exhaustiveVerify } from "../src/verify.ts";
import type { Op } from "../src/ops.ts";

const line = (s = "") => console.log(s);

// 流水线：仿射 → 量化 → 仿射 → 分段量化（负半轴粗网格 floor，正半轴细网格 half-even）
const ops: Op[] = [
  affine(fromString("1.5"), fromString("-0.25"), "scale-1.5"),
  quantizeOp(
    [{ lo: null, hi: null, step: fromString("0.5"), rule: "half-up" }],
    "round-0.5",
  ),
  affine(rat(2n), rat(1n), "boost-x2"),
  quantizeOp(
    [
      { lo: null, hi: rat(0n), step: rat(2n), rule: "floor" },
      { lo: rat(0n), hi: null, step: rat(1n), rule: "half-even" },
    ],
    "piecewise-quant",
  ),
];
const finalQuant = { step: rat(1n), rule: "half-even" } as const;

line("=== 流水线 ===");
ops.forEach((op, i) => line(`  op#${i} ${op.name ?? ""}: ${describeOp(op)}`));
line(`  累计误差界（stepwise 最坏）: ${rs(pipelineErrorBound(ops))}`);
line();

line("=== 1. 逐步执行与每步损失审计（x = 2）===");
for (const mode of ["stepwise", "deferred"] as const) {
  const r = runPipeline(ops, rat(2n), mode, finalQuant);
  line(`[${mode}]`);
  for (const s of r.steps)
    line(
      `  ${s.name.padEnd(16)} ${rs(s.input).padStart(8)} -> ${rs(s.output).padStart(8)}` +
      `  损失=${rs(s.loss)}  上界=${rs(s.maxLoss)}${s.deferred ? "  (延迟，未舍入)" : ""}`,
    );
  line(`  输出=${rs(r.output)}  精确值=${rs(r.exactValue)}  累计误差界=${rs(r.errorBound)}`);
}
line();

line("=== 2. 双模式对照与差异原因 ===");
for (const i of [0n, 1n, 2n, 3n, -1n, -3n]) {
  const c = compareModes(ops, rat(i), finalQuant);
  line(
    `  x=${i.toString().padStart(2)}  stepwise=${rs(c.stepwise.output).padStart(6)}` +
    `  deferred=${rs(c.deferred.output).padStart(6)}  ${c.differs ? "不同" : "相同"}`,
  );
  if (c.differs) line(`    原因: ${c.reason}`);
}
line();

line("=== 3. 输出区间的完整输入原像 ===");
const target = interval(rat(5n), true, rat(7n), false); // [5, 7)
line(`  输出区间 Y = ${intervalToString(target)}`);
for (const iv of pipelinePreimage(ops, target)) line(`    原像: ${intervalToString(iv)}`);
line();

line("=== 4. 复合简化（舍入步骤不移动）===");
const mergeable: Op[] = [
  affine(rat(2n), rat(1n), "a1"),
  affine(fromString("0.5"), rat(3n), "a2"),
  affine(rat(4n), fromString("-1.5"), "a3"),
  quantizeOp([{ lo: null, hi: null, step: rat(1n), rule: "half-up" }], "round"),
  affine(rat(3n), rat(0n), "a4"),
  affine(rat(1n), rat(2n), "a5"),
];
line("  原始: " + mergeable.map((op, i) => `op#${i}:${op.name}`).join(" "));
const simp = simplifyOps(mergeable);
simp.log.forEach((l) => line(`  ${l}`));
line(`  简化后 op 数: ${mergeable.length} -> ${simp.ops.length}`);
simp.ops.forEach((op, i) => line(`    op#${i} ${describeOp(op)}`));
line();

line("=== 5. 整数网格穷举核验（x ∈ [-8, 8]）===");
const report = exhaustiveVerify(ops, -8n, 8n, finalQuant);
line(`  核验输入数: ${report.checked}`);
line(`  失败数: ${report.failures.length}`);
report.failures.slice(0, 5).forEach((f) => line(`    ${f}`));
line(`  累计误差界: ${rs(report.errorBound)}  实际最大偏差: ${rs(report.maxObservedDeviation)}`);
if (report.minDiff) {
  line(`  最小差异输入: x=${rs(report.minDiff.input)}`);
  line(`    stepwise=${rs(report.minDiff.stepwise)}  deferred=${rs(report.minDiff.deferred)}`);
  line(`    原因: ${report.minDiff.reason}`);
} else {
  line("  网格内两模式无差异");
}
