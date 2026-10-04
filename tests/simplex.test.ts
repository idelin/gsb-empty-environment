import { test } from "node:test";
import assert from "node:assert/strict";
import { solve } from "../src/simplex.ts";
import { checkResult } from "../src/checker.ts";
import { formatRat, parseRat } from "../src/rational.ts";
import { lp } from "./helpers.ts";

function solveAndCheck(problem: ReturnType<typeof lp>, budget?: number) {
  const result = solve(problem, budget);
  if (result.status !== "iteration_limit") {
    const check = checkResult(problem, result);
    assert.deepEqual(check.violations, [], `certificate check failed for ${result.status}`);
    assert.ok(check.ok);
  }
  return result;
}

test("基本有界最优（含分数最优值）", () => {
  // max 3x+2y, x+2y<=6, 2x+y<=6 -> 交点 (2,2)，值 10
  const problem = lp(["3", "2"], [["1", "2"], ["2", "1"]], ["6", "6"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "10");
});

test("分数系数产生非整数最优值", () => {
  // max x+y, 2x+y<=4, x+2y<=4 -> (4/3, 4/3)，值 8/3
  const problem = lp(["1", "1"], [["2", "1"], ["1", "2"]], ["4", "4"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "8/3");
  assert.deepEqual(r.x!.map(formatRat), ["4/3", "4/3"]);
});

test("负 b 触发第一阶段", () => {
  // max x, -x <= -2 (即 x>=2), x <= 5 -> x=5
  const problem = lp(["1"], [["-1"], ["1"]], ["-2", "5"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "5");
  assert.deepEqual(r.x!.map(formatRat), ["5"]);
});

test("不可行 + Farkas 证书", () => {
  // x <= 1 且 x >= 2
  const problem = lp(["1"], [["1"], ["-1"]], ["1", "-2"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "infeasible");
  assert.ok(r.y!.every((v) => v.num >= 0n));
});

test("无界 + 射线证书", () => {
  // max x+y, x-y<=1
  const problem = lp(["1", "1"], [["1", "-1"]], ["1"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "unbounded");
  assert.ok(r.x !== undefined);
  assert.ok(r.direction!.some((v) => v.num > 0n));
});

test("退化顶点：多约束共点，Bland 规则正常终止", () => {
  // (1,1) 处 x1+x2=2, x1=1, x2=1 三约束同时活跃（2 维中 3 个活跃约束）
  const problem = lp(
    ["1", "1"],
    [["1", "1"], ["1", "0"], ["0", "1"], ["2", "1"]],
    ["2", "1", "1", "3"],
  );
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "2");
  assert.ok(r.iterations < 50);
});

test("Beale 循环示例：Bland 规则不循环并求得精确最优 1/20", () => {
  const problem = lp(
    ["3/4", "-150", "1/50", "-6"],
    [
      ["1/4", "-60", "-1/25", "9"],
      ["1/2", "-90", "-1/50", "3"],
      ["0", "0", "1", "0"],
    ],
    ["0", "0", "1"],
  );
  const r = solveAndCheck(problem, 10_000);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "1/20");
  assert.ok(r.iterations < 100, `iterations=${r.iterations}`);
});

test("多重最优：最优面为线段", () => {
  // max x+y, x+y<=1 -> 线段上每点最优
  const problem = lp(["1", "1"], [["1", "1"]], ["1"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "1");
  assert.equal(r.hasAlternativeOptima, true);
});

test("多重最优：手工点 (1/2,1/2) 也被检查器接受", () => {
  const problem = lp(["1", "1"], [["1", "1"]], ["1"]);
  const check = checkResult(problem, {
    status: "optimal",
    iterations: 0,
    x: [parseRat("1/2"), parseRat("1/2")],
    y: [parseRat("1")],
  });
  assert.ok(check.ok, check.violations.join("; "));
});

test("冗余行：重复约束与零行不影响结果", () => {
  const problem = lp(
    ["1", "1"],
    [
      ["1", "1"],
      ["1", "1"],
      ["0", "0"],
      ["2", "2"],
    ],
    ["4", "4", "5", "8"],
  );
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "4");
});

test("冗余零行且 b<0 时不可行", () => {
  // 0 <= -1 直接不可行
  const problem = lp(["1"], [["0"]], ["-1"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "infeasible");
});

test("零变量：b>=0 可行，最优值 0", () => {
  const problem = lp([], [[], []], ["3", "0"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "0");
  assert.deepEqual(r.x, []);
});

test("零变量：存在负 b 不可行", () => {
  const problem = lp([], [[], []], ["1", "-2"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "infeasible");
});

test("零约束：c 有正分量则无界", () => {
  const problem = lp(["1", "-2"], [], []);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "unbounded");
  assert.deepEqual(r.direction!.map(formatRat), ["1", "0"]);
});

test("零约束：c<=0 时原点最优", () => {
  const problem = lp(["-1", "-2"], [], []);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "0");
});

test("迭代预算耗尽：单独报告，不伪称不可行", () => {
  // max x, x<=1 需要至少 1 次枢轴
  const problem = lp(["1"], [["1"]], ["1"]);
  const r = solve(problem, 0);
  assert.equal(r.status, "iteration_limit");
  assert.equal(r.iterations, 0);
  // 预算恰好足够则能解出
  const r2 = solveAndCheck(problem, 1);
  assert.equal(r2.status, "optimal");
  assert.equal(formatRat(r2.objective!), "1");
});

test("第一阶段中预算耗尽也报告 iteration_limit", () => {
  // 需要第一阶段（负 b）且至少一次枢轴
  const problem = lp(["1"], [["-1"], ["1"]], ["-2", "5"]);
  const r = solve(problem, 0);
  assert.equal(r.status, "iteration_limit");
});

test("对偶证书：手工最优解与对偶通过强对偶", () => {
  // max 2x+3y, x+y<=4, x+3y<=6 -> x=(3,1), y=(3/2,1/2), 值 9
  const problem = lp(["2", "3"], [["1", "1"], ["1", "3"]], ["4", "6"]);
  const r = solveAndCheck(problem);
  assert.equal(r.status, "optimal");
  assert.equal(formatRat(r.objective!), "9");
  // 手工对偶也应通过
  const manual = checkResult(problem, {
    status: "optimal",
    iterations: 0,
    x: [parseRat("3"), parseRat("1")],
    y: [parseRat("3/2"), parseRat("1/2")],
  });
  assert.ok(manual.ok, manual.violations.join("; "));
});

test("无界射线：手工可行点与方向通过检查", () => {
  const problem = lp(["1", "1"], [["1", "-1"]], ["1"]);
  const manual = checkResult(problem, {
    status: "unbounded",
    iterations: 0,
    x: [parseRat("0"), parseRat("0")],
    direction: [parseRat("1"), parseRat("1")],
  });
  assert.ok(manual.ok, manual.violations.join("; "));
});

test("不可行证书：手工 Farkas 向量通过检查", () => {
  const problem = lp(["1"], [["1"], ["-1"]], ["1", "-2"]);
  const manual = checkResult(problem, {
    status: "infeasible",
    iterations: 0,
    y: [parseRat("1"), parseRat("1")],
  });
  assert.ok(manual.ok, manual.violations.join("; "));
});
