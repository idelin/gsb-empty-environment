import { test } from "node:test";
import assert from "node:assert/strict";
import { rat, fromString, toString as rs } from "../src/rational.ts";
import { affine, quantizeOp, applyOp } from "../src/ops.ts";
import type { Op } from "../src/ops.ts";
import { simplifyOps, checkSimplified } from "../src/simplify.ts";

const fwd = (ops: Op[], x: Parameters<typeof applyOp>[1]) => {
  let v = x;
  for (const op of ops) v = applyOp(op, v);
  return v;
};

test("相邻仿射被复合，语义不变", () => {
  const ops: Op[] = [
    affine(rat(2n), rat(1n)),
    affine(fromString("0.5"), rat(3n)),
    affine(rat(4n), fromString("-1.5")),
  ];
  const { ops: out, log } = simplifyOps(ops);
  assert.equal(out.length, 1);
  const only = out[0];
  assert.equal(only.kind, "affine");
  // 4*(0.5*(2x+1)+3)-1.5 = 4x + 12.5
  assert.ok(only.kind === "affine");
  assert.equal(rs(only.scale), "4");
  assert.equal(rs(only.offset), "12.5");
  assert.ok(log.some((l) => l.includes("合并相邻仿射 op#0..op#2")));
  const inputs = Array.from({ length: 21 }, (_, i) => rat(BigInt(i - 10)));
  assert.deepEqual(checkSimplified(ops, out, inputs, fwd), []);
});

test("舍入步骤不被移动、合并或删除", () => {
  const ops: Op[] = [
    affine(rat(2n), rat(0n)),
    quantizeOp([{ lo: null, hi: null, step: rat(1n), rule: "half-up" }], "q1"),
    affine(rat(3n), rat(1n)),
    quantizeOp([{ lo: null, hi: null, step: fromString("0.5"), rule: "floor" }], "q2"),
    affine(rat(1n), rat(0n)), // 恒等
  ];
  const { ops: out, log } = simplifyOps(ops);
  assert.equal(out.filter((o) => o.kind === "quantize").length, 2);
  assert.equal(out[1].kind, "quantize");
  assert.equal(out[2].kind, "affine"); // q1 与 q2 之间的仿射保持原位
  assert.equal(out[3].kind, "quantize");
  assert.ok(log.filter((l) => l.includes("保持原位")).length === 2);
  // 恒等仿射被删除但日志说明
  assert.ok(log.some((l) => l.includes("恒等")));
  const inputs = Array.from({ length: 41 }, (_, i) => rat(BigInt(i - 20), 2n));
  assert.deepEqual(checkSimplified(ops, out, inputs, fwd), []);
});

test("复合绝不跨越舍入步骤（顺序敏感场景）", () => {
  // round(2x) 与 2*round(x) 不同；简化不得把仿射挪过 round
  const ops: Op[] = [
    affine(rat(2n), rat(0n)),
    quantizeOp([{ lo: null, hi: null, step: rat(1n), rule: "half-up" }]),
  ];
  const { ops: out } = simplifyOps(ops);
  assert.equal(out.length, 2); // 无法合并
  // 2.6 -> round(5.2) = 5；若错误地先合并会改变结果
  assert.equal(fwd(out, fromString("2.6")).n, 5n);
});
