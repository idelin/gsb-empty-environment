/** 验收测试:任务排期场景(5 软约束 + 3 硬约束)。 */

import assert from "node:assert/strict";
import { test } from "node:test";
import { bruteForce, solve, totalPenalty } from "../src/solver.ts";
import { buildSchedulingProblem } from "../src/scheduling.ts";

const problem = buildSchedulingProblem();
const result = solve(problem);

test("存在可行解且所有硬约束绝对成立", () => {
  assert.ok(result.assignment, "应找到可行排期");
  for (const hc of problem.hard) {
    assert.ok(
      hc.consistent(result.assignment!),
      `硬约束 ${hc.name} 必须成立`,
    );
  }
});

test("全局最优:恰好违反 2 条最次要的软约束 S4、S5", () => {
  assert.deepEqual([...result.violated].sort(), ["S4", "S5"]);
});

test("总惩罚最小,等于理论下界 3 (S4=2 + S5=1)", () => {
  assert.equal(result.cost, 3);
  assert.equal(totalPenalty(problem.soft, result.assignment!), 3);
});

test("分支定界结果与暴力枚举的全局最优一致", () => {
  const bf = bruteForce(problem);
  assert.equal(result.cost, bf.cost);
});

test("剪枝有效:展开节点数远小于全空间叶节点数 243", () => {
  assert.ok(
    result.stats.nodes < 3 ** 5,
    `展开节点 ${result.stats.nodes} 应小于 243`,
  );
  assert.ok(result.stats.pruned > 0, "应发生剪枝");
});

test("最优解不违反权重更高的 S1/S2/S3", () => {
  for (const name of ["S1", "S2", "S3"]) {
    assert.ok(!result.violated.includes(name), `${name} 不应被违反`);
  }
});
