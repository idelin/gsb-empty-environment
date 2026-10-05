import { test } from "node:test";
import assert from "node:assert/strict";
import { solve } from "../src/solver.ts";
import type { Instance } from "../src/types.ts";

/** 独立的暴力枚举参照实现：返回最小成本，不可行为 null。 */
function bruteForceMinCost(inst: Instance): number | null {
  const names = Object.keys(inst.variables);
  const mutable = Object.keys(inst.mutable ?? {});
  const k = mutable.length;
  let best: number | null = null;
  for (let mask = 0; mask < 1 << k; mask++) {
    const assignment: Record<string, boolean> = { ...inst.variables };
    let cost = 0;
    for (let i = 0; i < k; i++) {
      if (mask & (1 << i)) {
        assignment[mutable[i]] = !assignment[mutable[i]];
        cost += (inst.mutable as Record<string, number>)[mutable[i]];
      }
    }
    const ok = inst.rules.every((rule) => {
      const premiseHolds = rule.if.every((lit) => assignment[lit.var] === lit.value);
      return !premiseHolds || assignment[rule.then.var] === rule.then.value;
    });
    if (ok && (best === null || cost < best)) best = cost;
  }
  return best;
}

test("无需修复：原赋值已满足全部规则", () => {
  const result = solve({
    variables: { a: true, b: true },
    mutable: { a: 1, b: 1 },
    rules: [{ if: [{ var: "a", value: true }], then: { var: "b", value: true } }],
  });
  assert.equal(result.status, "satisfied");
  assert.equal(result.totalCost, 0);
  assert.deepEqual(result.changes, []);
  assert.ok(result.verification.every((v) => v.satisfied));
});

test("翻转结论变量更便宜", () => {
  const result = solve({
    variables: { a: true, b: false },
    mutable: { a: 5, b: 1 },
    rules: [{ if: [{ var: "a", value: true }], then: { var: "b", value: true } }],
  });
  assert.equal(result.status, "repaired");
  assert.equal(result.totalCost, 1);
  assert.deepEqual(result.changes, [{ var: "b", from: false, to: true, cost: 1 }]);
  assert.ok(result.verification.every((v) => v.satisfied));
});

test("翻转前提变量更便宜（使前提不成立）", () => {
  const result = solve({
    variables: { a: true, b: false },
    mutable: { a: 1, b: 5 },
    rules: [{ if: [{ var: "a", value: true }], then: { var: "b", value: true } }],
  });
  assert.equal(result.status, "repaired");
  assert.equal(result.totalCost, 1);
  assert.deepEqual(result.changes, [{ var: "a", from: true, to: false, cost: 1 }]);
});

test("一次翻转同时修复多条规则", () => {
  const result = solve({
    variables: { a: true, b: false, c: false },
    mutable: { a: 3, b: 1, c: 1 },
    rules: [
      { if: [{ var: "a", value: true }], then: { var: "b", value: true } },
      { if: [{ var: "a", value: true }], then: { var: "c", value: true } },
    ],
  });
  assert.equal(result.status, "repaired");
  assert.equal(result.totalCost, 2);
  assert.deepEqual(
    result.changes.map((c) => c.var).sort(),
    ["b", "c"],
  );
});

test("无解：输出不可修改变量构成的极小冲突核心", () => {
  const result = solve({
    variables: { a: true, m: false, c: true, d: false },
    mutable: { m: 1, d: 1 },
    rules: [
      { id: "r1", if: [{ var: "a", value: true }], then: { var: "m", value: true } },
      { id: "r2", if: [{ var: "a", value: true }], then: { var: "m", value: false } },
      { id: "r3", if: [{ var: "c", value: true }], then: { var: "d", value: true } },
    ],
  });
  assert.equal(result.status, "unsatisfiable");
  if (result.status !== "unsatisfiable") return;
  // r1 与 r2 共同要求 m 既真又假，单独各自都可修复；r3 可修复，不应留在核心中。
  assert.deepEqual(
    result.conflictCore.map((r) => r.id).sort(),
    ["r1", "r2"],
  );
  assert.deepEqual(result.immutableVariablesInCore, ["a"]);
});

test("随机实例与暴力枚举对拍（成本与可满足性）", () => {
  let seed = 12345;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let iter = 0; iter < 300; iter++) {
    const n = 1 + Math.floor(rand() * 7); // 1..7 个变量
    const names = Array.from({ length: n }, (_, i) => `v${i}`);
    const variables: Record<string, boolean> = {};
    for (const name of names) variables[name] = rand() < 0.5;
    const mutable: Record<string, number> = {};
    for (const name of names) if (rand() < 0.6) mutable[name] = Math.floor(rand() * 6);
    const ruleCount = 1 + Math.floor(rand() * 6);
    const rules = Array.from({ length: ruleCount }, () => {
      const premiseSize = Math.floor(rand() * 3);
      const premise = Array.from({ length: premiseSize }, () => ({
        var: names[Math.floor(rand() * n)],
        value: rand() < 0.5,
      })).filter((lit, i, arr) => arr.findIndex((o) => o.var === lit.var) === i);
      return {
        if: premise,
        then: { var: names[Math.floor(rand() * n)], value: rand() < 0.5 },
      };
    });
    const inst: Instance = { variables, mutable, rules };
    const expected = bruteForceMinCost(inst);
    const result = solve(inst);
    if (expected === null) {
      assert.equal(result.status, "unsatisfiable", `iter ${iter}: 应判无解`);
      if (result.status !== "unsatisfiable") continue;
      // 核心必须极小：去掉任意一条后变得可行。
      for (const coreRule of result.conflictCore) {
        const reduced = inst.rules.filter((_, i) =>
          result.status === "unsatisfiable" &&
          i !== coreRule.rule &&
          result.conflictCore.some((c) => c.rule === i),
        );
        assert.notEqual(
          bruteForceMinCost({ ...inst, rules: reduced }),
          null,
          `iter ${iter}: 核心去掉规则 ${coreRule.rule} 后应可行`,
        );
      }
    } else {
      assert.notEqual(result.status, "unsatisfiable", `iter ${iter}: 应有解`);
      if (result.status === "unsatisfiable") continue;
      assert.equal(result.totalCost, expected, `iter ${iter}: 成本应最优`);
      assert.ok(
        result.verification.every((v) => v.satisfied),
        `iter ${iter}: 修复后所有规则必须满足`,
      );
      // 复核改动与最终赋值一致。
      for (const change of result.changes) {
        assert.equal(variables[change.var], change.from);
        assert.equal(result.assignment[change.var], change.to);
      }
    }
  }
});
