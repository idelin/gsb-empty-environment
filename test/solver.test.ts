import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { test } from "node:test";
import { compileProblem } from "../src/parse.ts";
import { checkRules, solveProblem } from "../src/solve.ts";
import { search } from "../src/solver.ts";
import type { CompiledProblem, ProblemInput } from "../src/types.ts";

function compile(input: ProblemInput): CompiledProblem {
  return compileProblem(input);
}

/** 暴力枚举所有可修改变量的翻转组合，返回最低成本（无解返回 Infinity）。 */
function bruteForceMinCost(problem: CompiledProblem): number {
  const modifiable: number[] = [];
  for (let v = 0; v < problem.varNames.length; v++) {
    if (!problem.fixed[v]) modifiable.push(v);
  }
  let best = Number.POSITIVE_INFINITY;
  const total = 1 << modifiable.length;
  const values = new Uint8Array(problem.varNames.length);
  for (let mask = 0; mask < total; mask++) {
    values.set(problem.initial);
    let cost = 0;
    for (let bit = 0; bit < modifiable.length; bit++) {
      if ((mask >> bit) & 1) {
        const v = modifiable[bit];
        values[v] = 1 - values[v];
        cost += problem.flipCost[v];
      }
    }
    if (cost >= best) continue;
    const checks = checkRules(problem, problem.rules, values);
    if (checks.every((c) => c.satisfied)) best = cost;
  }
  return best;
}

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("单条规则：翻转结论变量即可修复", () => {
  const problem = compile({
    variables: ["a", "b"],
    assignment: { a: true, b: false },
    modifiable: { b: 2 },
    rules: [{ name: "r1", premise: ["a"], conclusion: "b" }],
  });
  const result = solveProblem(problem);
  assert.equal(result.satisfiable, true);
  assert.equal(result.cost, 2);
  assert.deepEqual(result.flips, [{ variable: "b", from: false, to: true, cost: 2 }]);
  assert.equal(result.ruleChecks.length, 1);
  assert.equal(result.ruleChecks[0].satisfied, true);
});

test("优先选择成本更低的修复路径", () => {
  // 规则 !a -> b 被违反：可翻转 a（成本 10）使前提为假，或翻转 b（成本 1）使结论为真。
  const problem = compile({
    variables: ["a", "b"],
    assignment: { a: false, b: false },
    modifiable: { a: 10, b: 1 },
    rules: [{ name: "r1", premise: ["!a"], conclusion: "b" }],
  });
  const result = solveProblem(problem);
  assert.equal(result.satisfiable, true);
  assert.equal(result.cost, 1);
  assert.deepEqual(
    result.flips.map((f) => f.variable),
    ["b"],
  );
});

test("原始赋值已满足时成本为 0 且无改动", () => {
  const problem = compile({
    variables: ["a", "b"],
    assignment: { a: true, b: true },
    modifiable: { a: 1, b: 1 },
    rules: [{ premise: ["a"], conclusion: "b" }],
  });
  const result = solveProblem(problem);
  assert.equal(result.satisfiable, true);
  assert.equal(result.cost, 0);
  assert.equal(result.flips.length, 0);
  assert.equal(result.ruleChecks[0].name, "r1");
});

test("负字面量结论与空前提", () => {
  const problem = compile({
    variables: ["a", "b"],
    assignment: { a: true, b: true },
    modifiable: { a: 3, b: 5 },
    rules: [
      { name: "r1", premise: [], conclusion: "!b" },
      { name: "r2", premise: ["!b"], conclusion: "a" },
    ],
  });
  const result = solveProblem(problem);
  assert.equal(result.satisfiable, true);
  assert.equal(result.cost, 5);
  assert.deepEqual(
    result.flips.map((f) => f.variable),
    ["b"],
  );
  assert.equal(result.finalAssignment.b, false);
  assert.equal(result.finalAssignment.a, true);
});

test("不可修改变量导致无解时给出最小冲突核心", () => {
  const problem = compile({
    variables: ["a", "x", "y"],
    assignment: { a: true, x: false, y: false },
    modifiable: { x: 1, y: 2 },
    rules: [
      { name: "r1", premise: ["a"], conclusion: "x" },
      { name: "r2", premise: ["x"], conclusion: "!a" },
      { name: "r3", premise: ["a"], conclusion: "y" },
    ],
  });
  const result = solveProblem(problem);
  assert.equal(result.satisfiable, false);
  assert.deepEqual(
    result.core.map((r) => r.name).sort(),
    ["r1", "r2"],
  );
  // 核心极小性：去掉任意一条后即可满足。
  for (const dropped of result.core) {
    const rest = problem.rules.filter((r) => r.name !== dropped.name);
    assert.equal(search(problem, rest, "exists").satisfiable, true);
  }
});

test("固定变量直接违反空前提规则时核心为该规则本身", () => {
  const problem = compile({
    variables: ["a", "b"],
    assignment: { a: true, b: false },
    modifiable: { a: 1 },
    rules: [
      { name: "r1", premise: [], conclusion: "b" },
      { name: "r2", premise: ["b"], conclusion: "a" },
    ],
  });
  const result = solveProblem(problem);
  assert.equal(result.satisfiable, false);
  assert.deepEqual(
    result.core.map((r) => r.name),
    ["r1"],
  );
});

test("随机小实例与暴力枚举结果一致", () => {
  const rand = mulberry32(20261005);
  for (let round = 0; round < 60; round++) {
    const varCount = 2 + Math.floor(rand() * 7); // 2..8 个变量
    const variables = Array.from({ length: varCount }, (_, i) => `v${i}`);
    const assignment: Record<string, boolean> = {};
    const modifiable: Record<string, number> = {};
    for (const name of variables) {
      assignment[name] = rand() < 0.5;
      if (rand() < 0.7) modifiable[name] = 1 + Math.floor(rand() * 5);
    }
    const ruleCount = 1 + Math.floor(rand() * 6);
    const rules = Array.from({ length: ruleCount }, (_, i) => {
      const premiseSize = Math.floor(rand() * 3);
      const premise = Array.from({ length: premiseSize }, () => {
        const name = variables[Math.floor(rand() * varCount)];
        return rand() < 0.5 ? `!${name}` : name;
      });
      const conclVar = variables[Math.floor(rand() * varCount)];
      return {
        name: `r${i + 1}`,
        premise,
        conclusion: rand() < 0.5 ? `!${conclVar}` : conclVar,
      };
    });
    const problem = compile({ variables, assignment, modifiable, rules });
    const expected = bruteForceMinCost(problem);
    const result = solveProblem(problem);
    if (expected === Number.POSITIVE_INFINITY) {
      assert.equal(result.satisfiable, false, `第 ${round} 轮应无解`);
      // 核心本身必须不可满足，且为极小。
      assert.equal(search(problem, result.core, "exists").satisfiable, false);
      for (const dropped of result.core) {
        const rest = result.core.filter((r) => r !== dropped);
        assert.equal(search(problem, rest, "exists").satisfiable, true);
      }
    } else {
      assert.equal(result.satisfiable, true, `第 ${round} 轮应有解`);
      assert.equal(result.cost, expected, `第 ${round} 轮成本应最优`);
      assert.ok(result.ruleChecks.every((c) => c.satisfied));
      const flipSum = result.flips.reduce((sum, f) => sum + f.cost, 0);
      assert.equal(flipSum, result.cost);
    }
  }
});

test("25 个变量上限内可求解", () => {
  const variables = Array.from({ length: 25 }, (_, i) => `v${i}`);
  const assignment: Record<string, boolean> = {};
  const modifiable: Record<string, number> = {};
  for (const name of variables) {
    assignment[name] = false;
    modifiable[name] = 1;
  }
  const rules = [
    { name: "r1", premise: ["!v0"], conclusion: "v1" },
    { name: "r2", premise: ["!v1"], conclusion: "v2" },
    { name: "r3", premise: ["v2", "!v3"], conclusion: "v4" },
    { name: "r4", premise: ["!v24"], conclusion: "v23" },
  ];
  const result = solveProblem(compile({ variables, assignment, modifiable, rules }));
  assert.equal(result.satisfiable, true);
  assert.ok(result.ruleChecks.every((c) => c.satisfied));
});

test("输入校验：变量数超过 25 报错", () => {
  const variables = Array.from({ length: 26 }, (_, i) => `v${i}`);
  const assignment = Object.fromEntries(variables.map((v) => [v, false]));
  assert.throws(
    () => compile({ variables, assignment, rules: [] }),
    /超过上限 25/,
  );
});

test("输入校验：规则引用未声明变量报错", () => {
  assert.throws(
    () =>
      compile({
        variables: ["a"],
        assignment: { a: true },
        rules: [{ premise: ["b"], conclusion: "a" }],
      }),
    /未声明的变量：b/,
  );
});

test("输入校验：非法字面量与重复规则名", () => {
  assert.throws(
    () =>
      compile({
        variables: ["a"],
        assignment: { a: true },
        rules: [{ premise: ["!!a"], conclusion: "a" }],
      }),
    /非法的字面量/,
  );
  assert.throws(
    () =>
      compile({
        variables: ["a"],
        assignment: { a: true },
        rules: [
          { name: "r", premise: ["a"], conclusion: "a" },
          { name: "r", premise: ["a"], conclusion: "!a" },
        ],
      }),
    /规则名重复/,
  );
});

test("CLI：示例文件端到端运行", () => {
  const ok = execFileSync(process.execPath, ["src/cli.ts", "examples/solvable.json"], {
    encoding: "utf8",
  });
  assert.match(ok, /修复成功：最低成本 = 4/);
  assert.match(ok, /规则复核/);

  let unsatOutput = "";
  let unsatCode = 0;
  try {
    unsatOutput = execFileSync(process.execPath, ["src/cli.ts", "examples/unsolvable.json"], {
      encoding: "utf8",
    });
  } catch (err) {
    unsatOutput = (err as { stdout: string }).stdout;
    unsatCode = (err as { status: number }).status;
  }
  assert.equal(unsatCode, 1);
  assert.match(unsatOutput, /无法修复/);
  assert.match(unsatOutput, /冲突核心/);
});
