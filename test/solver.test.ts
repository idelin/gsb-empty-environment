import { test } from "node:test";
import assert from "node:assert/strict";
import { isSatisfied } from "../src/csp.ts";
import { solve } from "../src/solver.ts";
import {
  buildBenchmarkProblem,
  buildUnsatisfiableProblem,
} from "../src/problem.ts";

test("CBJ 求解 10 变量基准问题，解满足全部约束", () => {
  const problem = buildBenchmarkProblem();
  const { solution } = solve(problem, "cbj");
  assert.ok(solution, "应找到解");
  assert.equal(solution.length, 10);
  assert.ok(isSatisfied(problem, solution), "解必须满足所有约束");
});

test("第 8 步发生冲突，且冲突集只包含第 2 步的变量", () => {
  const problem = buildBenchmarkProblem();
  const { events } = solve(problem, "cbj");

  const deadEnd = events.find((e) => e.type === "dead-end");
  assert.ok(deadEnd && deadEnd.type === "dead-end");
  assert.equal(deadEnd.level, 8, "死胡同应发生在第 8 步");
  assert.equal(deadEnd.variable, "X8");
  assert.deepEqual(deadEnd.conflictSet, [2], "冲突集应只含第 2 步 (X2)");
});

test("死胡同后直接跳过第 3..7 步回跳到第 2 步", () => {
  const problem = buildBenchmarkProblem();
  const { stats, events } = solve(problem, "cbj");

  assert.equal(stats.backjumps.length, 1, "整个求解只应发生一次回跳");
  const jump = stats.backjumps[0];
  assert.equal(jump.from, 8);
  assert.equal(jump.to, 2);
  assert.equal(jump.width, 6, "一次跳过 6 层 (第 3..7 步及自身)");

  // 回跳事件之后，下一个赋值动作必须发生在第 2 层
  const jumpIdx = events.findIndex((e) => e.type === "backjump");
  const nextAssign = events.slice(jumpIdx + 1).find((e) => e.type === "assign");
  assert.ok(nextAssign && nextAssign.type === "assign");
  assert.equal(nextAssign.level, 2);
  assert.equal(nextAssign.variable, "X2");
});

test("CBJ 相比时序回溯极大剪除无用搜索分支", () => {
  const problem = buildBenchmarkProblem();
  const cbj = solve(problem, "cbj");
  const bt = solve(problem, "chronological");

  assert.deepEqual(bt.solution, cbj.solution, "两种算法应找到同一个首解");
  assert.ok(
    cbj.stats.nodes * 10 <= bt.stats.nodes,
    `CBJ 节点数 (${cbj.stats.nodes}) 应至少比时序回溯 (${bt.stats.nodes}) 少一个数量级`,
  );
  assert.ok(
    cbj.stats.constraintChecks * 10 <= bt.stats.constraintChecks,
    "约束检查次数同样应少一个数量级",
  );
});

test("不可满足变体：回跳至根后正确报告无解", () => {
  const problem = buildUnsatisfiableProblem();
  const { solution, events } = solve(problem, "cbj");
  assert.equal(solution, null);
  assert.ok(events.some((e) => e.type === "unsat"));
});

test("sanity：3 变量全不同问题两种模式均可解", () => {
  const problem = {
    varNames: ["A", "B", "C"],
    domains: [
      [1, 2, 3],
      [1, 2, 3],
      [1, 2, 3],
    ],
    constraints: [
      { a: 0, b: 1, name: "A != B", holds: (x: number, y: number) => x !== y },
      { a: 1, b: 2, name: "B != C", holds: (x: number, y: number) => x !== y },
      { a: 0, b: 2, name: "A != C", holds: (x: number, y: number) => x !== y },
    ],
  };
  for (const mode of ["cbj", "chronological"] as const) {
    const { solution } = solve(problem, mode);
    assert.ok(solution);
    assert.ok(isSatisfied(problem, solution));
  }
});
