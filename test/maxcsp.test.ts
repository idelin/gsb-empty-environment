/**
 * MaxCSPSolver 通用求解器测试（Node.js 内置 node:test）。
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { MaxCSPSolver, type Assignment } from "../src/maxcsp.ts";

/** 可复现的伪随机数生成器（mulberry32）。 */
function rng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** 独立暴力枚举：返回满足硬约束的最小软约束惩罚，不可行返回 null。 */
function bruteForce(
  variables: { name: string; domain: number[] }[],
  hard: ((a: Assignment) => boolean)[],
  soft: { weight: number; violated: (a: Assignment) => boolean | null }[],
): number | null {
  let best: number | null = null;
  const current: Record<string, number> = {};
  const visit = (depth: number): void => {
    if (depth === variables.length) {
      if (!hard.every((h) => h(current))) return;
      let cost = 0;
      for (const sc of soft) {
        if (sc.violated(current) === true) cost += sc.weight;
      }
      if (best === null || cost < best) best = cost;
      return;
    }
    const v = variables[depth]!;
    for (const value of v.domain) {
      current[v.name] = value;
      visit(depth + 1);
    }
    delete current[v.name];
  };
  visit(0);
  return best;
}

test("手工构造的小问题：返回已知全局最优", () => {
  // x, y ∈ {0,1,2}；硬约束 x != y；
  // 软约束：x=0 (w5)，y=0 (w3)，x+y=4 (w4)。
  // 全满足需 x=0,y=0 违反硬约束；最优为 x=2,y=0（违反 w5+w4=9）？
  // 枚举：x=0,y=2 → 违反 y=0(w3) 与 x+y=4(w4) = 7；x=1,y=0 → 违反 w5+w4 = 9；
  // x=2,y=0 → 9；x=0,y=1 → 3+4=7；故最优 = 7（两种取法）。
  const solver = new MaxCSPSolver();
  solver.addVariable("x", [0, 1, 2]);
  solver.addVariable("y", [0, 1, 2]);
  solver.addHardConstraint({
    name: "neq",
    consistent: (a) => a.x === undefined || a.y === undefined || a.x !== a.y,
  });
  solver.addSoftConstraint({ name: "x0", weight: 5, violated: (a) => (a.x === undefined ? null : a.x !== 0) });
  solver.addSoftConstraint({ name: "y0", weight: 3, violated: (a) => (a.y === undefined ? null : a.y !== 0) });
  solver.addSoftConstraint({
    name: "sum4",
    weight: 4,
    violated: (a) => (a.x === undefined || a.y === undefined ? null : (a.x as number) + (a.y as number) !== 4),
  });

  const solution = solver.solve();
  assert.ok(solution);
  assert.equal(solution.cost, 7);
  assert.equal(solution.assignment.x !== solution.assignment.y, true);
  const names = solution.violations.map((v) => v.name).sort();
  assert.deepEqual(names, ["sum4", "y0"]);
});

test("硬约束不可行时返回 null", () => {
  const solver = new MaxCSPSolver();
  solver.addVariable("x", [0, 1]);
  solver.addVariable("y", [0, 1]);
  solver.addHardConstraint({ name: "x-eq-0", consistent: (a) => a.x === undefined || a.x === 0 });
  solver.addHardConstraint({ name: "x-eq-1", consistent: (a) => a.x === undefined || a.x === 1 });
  assert.equal(solver.solve(), null);
});

test("随机实例：分支定界结果与暴力枚举一致（20 个种子）", () => {
  for (let seed = 1; seed <= 20; seed++) {
    const rand = rng(seed);
    const varCount = 5;
    const domain = [0, 1, 2, 3];
    const variables = Array.from({ length: varCount }, (_, i) => ({ name: `v${i}`, domain }));

    // 随机硬约束：随机变量对取值不等。
    const hardPairs: [string, string][] = [];
    const solver = new MaxCSPSolver();
    for (const v of variables) solver.addVariable(v.name, v.domain);
    for (let k = 0; k < 3; k++) {
      const i = Math.floor(rand() * varCount);
      const j = (i + 1 + Math.floor(rand() * (varCount - 1))) % varCount;
      hardPairs.push([`v${i}`, `v${j}`]);
    }
    for (const [a, b] of hardPairs) {
      solver.addHardConstraint({
        name: `${a}!=${b}`,
        consistent: (asg) => asg[a] === undefined || asg[b] === undefined || asg[a] !== asg[b],
      });
    }

    // 随机软约束：变量取某目标值，随机权重。
    const softDefs: { name: string; weight: number; violated: (a: Assignment) => boolean | null }[] = [];
    for (let k = 0; k < 6; k++) {
      const v = `v${Math.floor(rand() * varCount)}`;
      const target = Math.floor(rand() * domain.length);
      const weight = 1 + Math.floor(rand() * 9);
      softDefs.push({
        name: `s${k}`,
        weight,
        violated: (a) => (a[v] === undefined ? null : a[v] !== target),
      });
    }
    for (const sc of softDefs) solver.addSoftConstraint(sc);

    const hardFns = hardPairs.map(
      ([a, b]) =>
        (asg: Assignment): boolean =>
          asg[a] === undefined || asg[b] === undefined || asg[a] !== asg[b],
    );
    const expected = bruteForce(variables, hardFns, softDefs);
    const solution = solver.solve();

    if (expected === null) {
      assert.equal(solution, null, `seed=${seed} 应不可行`);
    } else {
      assert.ok(solution, `seed=${seed} 应有解`);
      assert.equal(solution.cost, expected, `seed=${seed} 最优代价应等于暴力枚举结果`);
      // 返回的解必须满足全部硬约束。
      for (const h of hardFns) assert.equal(h(solution.assignment), true);
      // 报告的违反集合权重和必须等于代价。
      assert.equal(
        solution.violations.reduce((acc, v) => acc + v.weight, 0),
        solution.cost,
      );
    }
  }
});

test("分支定界确实发生剪枝（未穷举整个空间）", () => {
  const solver = new MaxCSPSolver();
  for (let i = 0; i < 5; i++) solver.addVariable(`v${i}`, [0, 1, 2, 3, 4]);
  solver.addHardConstraint({
    name: "all-different",
    consistent: (a) => {
      const seen = new Set<unknown>();
      for (let i = 0; i < 5; i++) {
        const v = a[`v${i}`];
        if (v === undefined) continue;
        if (seen.has(v)) return false;
        seen.add(v);
      }
      return true;
    },
  });
  for (let i = 0; i < 5; i++) {
    solver.addSoftConstraint({
      name: `p${i}`,
      weight: i + 1,
      violated: (a) => (a[`v${i}`] === undefined ? null : a[`v${i}`] !== i),
    });
  }

  const solution = solver.solve();
  assert.ok(solution);
  assert.equal(solution.cost, 0);
  assert.ok(solution.stats.nodesPruned > 0, "应发生下界剪枝");
  assert.ok(solution.stats.hardPruned > 0, "应发生硬约束剪枝");
  assert.ok(
    solution.stats.leavesVisited < solution.stats.totalLeafSpace,
    `访问叶子 ${solution.stats.leavesVisited} 应远小于全空间 ${solution.stats.totalLeafSpace}`,
  );
});

test("非法定义被拒绝", () => {
  const solver = new MaxCSPSolver();
  assert.throws(() => solver.addVariable("x", []), /值域不能为空/);
  solver.addVariable("x", [1]);
  assert.throws(() => solver.addVariable("x", [2]), /重复定义/);
  assert.throws(
    () => solver.addSoftConstraint({ name: "bad", weight: 0, violated: () => null }),
    /权重必须为正数/,
  );
});
