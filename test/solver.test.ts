import { test } from "node:test";
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";
import type { Decision, Problem } from "../src/types.ts";
import { solve } from "../src/solver.ts";

/** 暴力枚举 3^n 全部方案，作为精确解的对照。 */
function bruteForce(problem: Problem): number {
  const rate = problem.recoveryRate ?? 1;
  const parts = [
    ...new Set([
      ...Object.keys(problem.parts),
      ...problem.devices.flatMap((d) => [...Object.keys(d.needs), ...Object.keys(d.contains)]),
    ]),
  ];
  let best = 0;
  const n = problem.devices.length;
  const choice = new Array<number>(n).fill(2);
  function evalChoice(): void {
    let cost = 0;
    let value = 0;
    const demand = new Map<string, number>();
    const supply = new Map<string, number>();
    for (const p of parts) supply.set(p, problem.parts[p]?.stock ?? 0);
    for (let i = 0; i < n; i++) {
      const d = problem.devices[i];
      if (choice[i] === 0) {
        cost += d.repairCost;
        value += d.repairedValue;
        for (const [p, q] of Object.entries(d.needs)) demand.set(p, (demand.get(p) ?? 0) + q);
      } else if (choice[i] === 1) {
        cost += d.dismantleCost ?? 0;
        for (const [p, q] of Object.entries(d.contains)) {
          supply.set(p, (supply.get(p) ?? 0) + Math.floor(q * (d.recoveryRate ?? rate)));
        }
      } else {
        value += d.residualValue;
      }
    }
    for (const p of parts) {
      const deficit = (demand.get(p) ?? 0) - (supply.get(p) ?? 0);
      if (deficit <= 0) continue;
      const price = problem.parts[p]?.price;
      if (price === undefined) return; // 备件不足且不可外购，方案不可行
      cost += deficit * price;
    }
    if (cost <= problem.budget && value > best) best = value;
  }
  (function walk(i: number): void {
    if (i === n) {
      evalChoice();
      return;
    }
    for (let c = 0; c < 3; c++) {
      choice[i] = c;
      walk(i + 1);
    }
  })(0);
  return best;
}

function mulberry32(seed: number): () => number {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomProblem(rand: () => number, n: number): Problem {
  const partNames = ["A", "B", "C"];
  const pick = () => Math.floor(rand() * 3);
  const bag = (max: number): Record<string, number> => {
    const out: Record<string, number> = {};
    for (const p of partNames) {
      const q = rand() < 0.5 ? 0 : 1 + pick() % max;
      if (q > 0) out[p] = q;
    }
    return out;
  };
  const parts: Problem["parts"] = {};
  for (const p of partNames) {
    parts[p] = rand() < 0.7 ? { stock: pick(), price: 3 + Math.floor(rand() * 10) } : { stock: pick() };
  }
  return {
    budget: 20 + Math.floor(rand() * 40),
    recoveryRate: 0.5 + Math.floor(rand() * 3) * 0.25,
    parts,
    devices: Array.from({ length: n }, (_, i) => ({
      id: `M${i}`,
      repairCost: Math.floor(rand() * 15),
      repairedValue: 10 + Math.floor(rand() * 60),
      residualValue: Math.floor(rand() * 12),
      dismantleCost: rand() < 0.3 ? Math.floor(rand() * 4) : 0,
      needs: bag(2),
      contains: bag(2),
    })),
  };
}

test("随机小规模案例与暴力枚举一致", () => {
  for (let seed = 1; seed <= 60; seed++) {
    const rand = mulberry32(seed);
    const problem = randomProblem(rand, 8);
    const sol = solve(problem);
    assert.equal(sol.totalValue, bruteForce(problem), `seed=${seed}`);
  }
});

test("拆解供件使修复可行，并产生 拆解机->修复机 流水", () => {
  const problem: Problem = {
    budget: 100,
    parts: { 齿轮: { stock: 0 } }, // 不可外购
    devices: [
      { id: "donor", repairCost: 1, repairedValue: 2, residualValue: 1,
        needs: {}, contains: { 齿轮: 2 } },
      { id: "target", repairCost: 5, repairedValue: 100, residualValue: 10,
        needs: { 齿轮: 1 }, contains: {} },
    ],
  };
  const sol = solve(problem);
  assert.equal(sol.decisions["donor"], "dismantle");
  assert.equal(sol.decisions["target"], "repair");
  assert.equal(sol.totalValue, 100);
  assert.ok(
    sol.flows.some((f) => f.from === "donor" && f.to === "target" && f.part === "齿轮" && f.qty === 1),
  );
});

test("修复与拆解互斥，且回收有损耗（floor）", () => {
  const problem: Problem = {
    budget: 100,
    recoveryRate: 0.5,
    parts: { 泵: { stock: 0 } },
    devices: [
      { id: "donor", repairCost: 1, repairedValue: 2, residualValue: 1,
        needs: {}, contains: { 泵: 3 } }, // 回收 floor(3*0.5)=1
      { id: "t1", repairCost: 5, repairedValue: 50, residualValue: 5,
        needs: { 泵: 1 }, contains: {} },
      { id: "t2", repairCost: 5, repairedValue: 50, residualValue: 5,
        needs: { 泵: 1 }, contains: {} },
    ],
  };
  const sol = solve(problem);
  assert.equal(sol.decisions["donor"], "dismantle");
  const repaired = [sol.decisions["t1"], sol.decisions["t2"]].filter((d) => d === "repair");
  assert.equal(repaired.length, 1, "损耗后只有 1 个泵，只能修复 1 台");
  assert.notEqual(sol.decisions["donor"], "repair", "同一台设备不能既修复又拆解");
});

test("预算约束生效", () => {
  const problem: Problem = {
    budget: 5,
    parts: {},
    devices: [
      { id: "a", repairCost: 10, repairedValue: 100, residualValue: 1, needs: {}, contains: {} },
      { id: "b", repairCost: 5, repairedValue: 60, residualValue: 1, needs: {}, contains: {} },
    ],
  };
  const sol = solve(problem);
  assert.equal(sol.decisions["a"], "idle");
  assert.equal(sol.decisions["b"], "repair");
  assert.ok(sol.budgetUsed <= 5);
});

test("备件流水无环（不能循环产料）", () => {
  const rand = mulberry32(7);
  for (let k = 0; k < 20; k++) {
    const problem = randomProblem(rand, 10);
    const sol = solve(problem); // buildFlows 内部会做拓扑校验，成环即抛错
    const repaired = new Set(
      Object.entries(sol.decisions).filter(([, d]) => d === "repair").map(([id]) => id),
    );
    for (const f of sol.flows) {
      if (f.from === "库存" || f.from === "采购") continue;
      assert.ok(!repaired.has(f.from), `被修复设备 ${f.from} 不得再作为备件来源`);
      assert.ok(repaired.has(f.to), `备件只能流向被修复设备，实际流向 ${f.to}`);
    }
  }
});

test("超过 18 台设备报错", () => {
  const problem: Problem = {
    budget: 1,
    parts: {},
    devices: Array.from({ length: 19 }, (_, i) => ({
      id: `x${i}`, repairCost: 0, repairedValue: 1, residualValue: 0, needs: {}, contains: {},
    })),
  };
  assert.throws(() => solve(problem), /超过上限/);
});

test("示例案例可复现", () => {
  const problem = JSON.parse(
    readFileSync(new URL("../examples/case.json", import.meta.url), "utf8"),
  ) as Problem;
  const sol = solve(problem);
  assert.equal(sol.totalValue, 438);
  assert.equal(sol.budgetUsed, 120);
});
