/**
 * 小规模随机实例的交叉验证：
 * 用精确顶点枚举（独立于单纯形法）计算有界实例的最优值，与求解器对比；
 * 所有返回的证书都用独立检查器做代数验证。
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { solve } from "../src/simplex.ts";
import { checkResult } from "../src/checker.ts";
import { cmp, formatRat } from "../src/rational.ts";
import { makeRng, randomLp, maxOverVertices, enumerateVertices } from "./helpers.ts";

test("随机小规模实例：与顶点枚举交叉验证", () => {
  const rng = makeRng(20261004);
  const counts = { optimal: 0, infeasible: 0, unbounded: 0 };
  const TRIALS = 400;
  for (let t = 0; t < TRIALS; t++) {
    const n = 1 + (rng() % 4); // 1..4 个变量
    const m = rng() % 7; // 0..6 个约束
    const problem = randomLp(rng, n, m, 4);
    const result = solve(problem, 100_000);
    assert.notEqual(
      result.status,
      "iteration_limit",
      `trial ${t}: unexpected iteration limit (n=${n}, m=${m})`,
    );
    counts[result.status as keyof typeof counts]++;

    // 任何状态下证书都必须通过独立代数检查
    const check = checkResult(problem, result);
    assert.deepEqual(
      check.violations,
      [],
      `trial ${t}: certificate invalid (n=${n}, m=${m}, status=${result.status})`,
    );

    const enumMax = maxOverVertices(problem);
    if (result.status === "optimal") {
      assert.ok(enumMax !== null, `trial ${t}: solver optimal but no feasible vertex`);
      assert.equal(
        cmp(result.objective!, enumMax),
        0,
        `trial ${t}: solver=${formatRat(result.objective!)} enum=${formatRat(enumMax)}`,
      );
    } else if (result.status === "infeasible") {
      assert.equal(enumMax, null, `trial ${t}: solver infeasible but vertex exists`);
    } else {
      // unbounded：区域必须非空（存在可行顶点）
      assert.ok(enumMax !== null, `trial ${t}: unbounded but no feasible point`);
    }
  }
  // 确保三类结果都被覆盖到
  assert.ok(counts.optimal > 0, "no optimal cases generated");
  assert.ok(counts.infeasible > 0, "no infeasible cases generated");
  assert.ok(counts.unbounded > 0, "no unbounded cases generated");
});

test("零变量随机实例：与直接判定一致", () => {
  const rng = makeRng(777);
  for (let t = 0; t < 50; t++) {
    const m = 1 + (rng() % 4);
    const problem = randomLp(rng, 0, m, 3);
    const result = solve(problem);
    const anyNegativeB = problem.b.some((bi) => bi.num < 0n);
    assert.equal(result.status, anyNegativeB ? "infeasible" : "optimal");
    const check = checkResult(problem, result);
    assert.deepEqual(check.violations, []);
  }
});

test("退化与冗余密集的实例：枚举交叉验证", () => {
  const rng = makeRng(42);
  for (let t = 0; t < 100; t++) {
    const n = 2;
    const m = 6; // 约束多于变量，退化顶点概率高
    const problem = randomLp(rng, n, m, 2);
    // 复制部分行制造冗余
    problem.A.push(problem.A[0].slice());
    problem.b.push(problem.b[0]);
    const result = solve(problem, 100_000);
    assert.notEqual(result.status, "iteration_limit");
    const check = checkResult(problem, result);
    assert.deepEqual(check.violations, []);
    if (result.status === "optimal") {
      const enumMax = maxOverVertices(problem);
      assert.ok(enumMax !== null);
      assert.equal(cmp(result.objective!, enumMax), 0);
    } else if (result.status === "infeasible") {
      assert.equal(enumerateVertices(problem).length, 0);
    }
  }
});
