/** 求解器单元测试 + 随机实例交叉验证(分支定界 vs 暴力枚举)。 */

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  bruteForce,
  solve,
  totalPenalty,
  type Assignment,
  type MaxCSPProblem,
} from "../src/solver.ts";

/** 确定性伪随机数生成器(mulberry32),保证测试可复现。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomProblem(rand: () => number): MaxCSPProblem {
  const varCount = 4 + Math.floor(rand() * 3); // 4..6 个变量
  const domain = [1, 2, 3];
  const names = Array.from({ length: varCount }, (_, i) => `x${i}`);
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];

  const hard = Array.from({ length: 3 }, (_, i) => {
    const a = pick(names);
    let b = pick(names);
    while (b === a) b = pick(names);
    return {
      name: `H${i}`,
      scope: [a, b],
      consistent: (asn: Assignment) =>
        asn[a] === undefined || asn[b] === undefined || asn[a] !== asn[b],
    };
  });

  const soft = Array.from({ length: 6 }, (_, i) => {
    const target = pick(domain);
    if (rand() < 0.5) {
      const a = pick(names);
      return {
        name: `S${i}`,
        weight: 1 + Math.floor(rand() * 10),
        scope: [a],
        violated: (asn: Assignment) => asn[a] !== undefined && asn[a] !== target,
      };
    }
    const a = pick(names);
    let b = pick(names);
    while (b === a) b = pick(names);
    return {
      name: `S${i}`,
      weight: 1 + Math.floor(rand() * 10),
      scope: [a, b],
      violated: (asn: Assignment) =>
        asn[a] !== undefined && asn[b] !== undefined && asn[a] !== asn[b],
    };
  });

  return {
    variables: names.map((name) => ({ name, domain })),
    hard,
    soft,
  };
}

test("随机实例交叉验证:分支定界代价 == 暴力枚举最优代价 (100 组)", () => {
  const rand = mulberry32(20261002);
  for (let i = 0; i < 100; i++) {
    const problem = randomProblem(rand);
    const bnb = solve(problem);
    const bf = bruteForce(problem);
    assert.equal(bnb.cost, bf.cost, `第 ${i} 组实例最优代价不一致`);
    if (bnb.assignment) {
      // 返回的赋值确实达到所报代价,且硬约束全部成立
      assert.equal(totalPenalty(problem.soft, bnb.assignment), bnb.cost);
      for (const hc of problem.hard) {
        assert.ok(hc.consistent(bnb.assignment));
      }
    } else {
      assert.equal(bf.assignment, null, "分支定界判不可行时枚举也应不可行");
    }
  }
});

test("硬约束不可满足时返回 null 与 Infinity", () => {
  const problem: MaxCSPProblem = {
    variables: [{ name: "x", domain: [1] }],
    hard: [
      {
        name: "H-impossible",
        scope: ["x"],
        consistent: (asn) => asn.x === undefined || asn.x !== 1,
      },
    ],
    soft: [],
  };
  const result = solve(problem);
  assert.equal(result.assignment, null);
  assert.equal(result.cost, Infinity);
  assert.deepEqual(result.violated, []);
});

test("无软约束时任意可行解代价为 0", () => {
  const problem: MaxCSPProblem = {
    variables: [
      { name: "a", domain: [1, 2] },
      { name: "b", domain: [1, 2] },
    ],
    hard: [
      {
        name: "H-neq",
        scope: ["a", "b"],
        consistent: (asn) =>
          asn.a === undefined || asn.b === undefined || asn.a !== asn.b,
      },
    ],
    soft: [],
  };
  const result = solve(problem);
  assert.equal(result.cost, 0);
  assert.ok(result.assignment);
  assert.notEqual(result.assignment!.a, result.assignment!.b);
});

test("全部软约束可满足时代价为 0 且无违反", () => {
  const problem: MaxCSPProblem = {
    variables: [{ name: "x", domain: [1, 2, 3] }],
    hard: [],
    soft: [
      {
        name: "S-prefer-2",
        weight: 5,
        scope: ["x"],
        violated: (asn) => asn.x !== undefined && asn.x !== 2,
      },
    ],
  };
  const result = solve(problem);
  assert.equal(result.cost, 0);
  assert.equal(result.assignment!.x, 2);
  assert.deepEqual(result.violated, []);
});
