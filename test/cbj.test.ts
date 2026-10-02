import { test } from "node:test";
import assert from "node:assert/strict";
import { solveCBJ, solveChronological, type CspProblem, type Value } from "../src/csp.ts";
import { buildDemoProblem } from "../src/demo-problem.ts";

function checkSolution(problem: CspProblem, assignment: readonly Value[]): void {
  for (const c of problem.constraints) {
    assert.ok(
      c.check(assignment[c.a], assignment[c.b]),
      `约束 ${c.label ?? `${c.a},${c.b}`} 被违反`,
    );
  }
}

test("CBJ 求得的解满足全部约束", () => {
  const problem = buildDemoProblem();
  const { assignment } = solveCBJ(problem);
  assert.ok(assignment, "应当有解");
  checkSolution(problem, assignment);
});

test("第 8 步冲突时直接回跳到第 2 步，跳过第 3~7 步", () => {
  const problem = buildDemoProblem();
  const { stats } = solveCBJ(problem);
  assert.ok(stats.backjumps.length >= 1, "应发生至少一次回跳");
  const jump = stats.backjumps[0];
  assert.equal(jump.fromLevel, 7, "冲突发生在第 8 步（0-based 7）");
  assert.equal(jump.toLevel, 1, "回跳目标是第 2 步（0-based 1）");
  assert.equal(jump.fromLevel - jump.toLevel - 1, 5, "跳过第 3~7 步共 5 层");
});

test("冲突分析：X8 的 conflict set 仅包含 X2", () => {
  const problem = buildDemoProblem();
  const { stats } = solveCBJ(problem);
  assert.deepEqual(stats.backjumps[0].conflictSet, [1]);
});

test("CBJ 相比时序回溯极大剪除搜索分支", () => {
  const problem = buildDemoProblem();
  const cbj = solveCBJ(problem);
  const chr = solveChronological(problem);
  assert.deepEqual(cbj.assignment, chr.assignment, "两种算法应找到同一字典序首解");
  assert.ok(
    cbj.stats.nodesVisited * 5 < chr.stats.nodesVisited,
    `CBJ(${cbj.stats.nodesVisited}) 应远少于时序回溯(${chr.stats.nodesVisited})`,
  );
});

test("时序回溯会在 X3..X7 上浪费 3^5 个无用组合", () => {
  const problem = buildDemoProblem();
  const chr = solveChronological(problem);
  // X3..X7 各 3 个取值，首路径 X8 失败后时序回溯需穷举这些组合才会动到 X2
  assert.ok(chr.stats.nodesVisited >= 3 ** 5);
});

test("不可满足问题返回 null", () => {
  const unsat: CspProblem = {
    numVars: 2,
    domains: [[1], [1]],
    constraints: [{ a: 0, b: 1, check: (x, y) => x !== y }],
  };
  const { assignment } = solveCBJ(unsat);
  assert.equal(assignment, null);
});

test("回跳后 conflict set 会合并到目标层（多根源冲突）", () => {
  // X1=1, X2=2, X3 ∈ {1,2}，约束 X3≠X1 且 X3≠X2 → X3 的 conflict set = {0,1}
  const problem: CspProblem = {
    numVars: 3,
    domains: [[1], [2], [1, 2]],
    constraints: [
      { a: 2, b: 0, check: (x3, x1) => x3 !== x1 },
      { a: 2, b: 1, check: (x3, x2) => x3 !== x2 },
    ],
  };
  const { assignment, stats } = solveCBJ(problem);
  assert.equal(assignment, null, "X1=X2=1 时 X3 无解");
  assert.deepEqual(stats.backjumps[0].conflictSet, [0, 1]);
  assert.equal(stats.backjumps[0].toLevel, 1, "跳到最深根源 X2");
});

test("无约束问题直接顺序赋值", () => {
  const problem: CspProblem = {
    numVars: 4,
    domains: [[1, 2], [1, 2], [1, 2], [1, 2]],
    constraints: [],
  };
  const { assignment, stats } = solveCBJ(problem);
  assert.deepEqual(assignment, [1, 1, 1, 1]);
  assert.equal(stats.backjumps.length, 0);
});
