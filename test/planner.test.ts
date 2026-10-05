import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  analyzeReachability,
  buildModel,
  enumerateLegalCombos,
  pairMaskOf,
  validateSpec,
} from "../src/core.ts";
import { solveExact, solveGreedy } from "../src/solvers.ts";
import { plan } from "../src/planner.ts";
import type { ProblemSpec } from "../src/types.ts";

function bruteForceCover(
  candidates: { pairMask: bigint; cost: number }[],
  reachableMask: bigint,
): number {
  let best = Infinity;
  const search = (uncovered: bigint, cost: number) => {
    if (uncovered === 0n) {
      if (cost < best) best = cost;
      return;
    }
    if (cost >= best) return;
    const pivot = uncovered & -uncovered;
    for (const c of candidates) {
      if ((c.pairMask & pivot) === 0n) continue;
      search(uncovered & ~c.pairMask, cost + c.cost);
    }
  };
  search(reachableMask, 0);
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

function randomSpec(rand: () => number, n: number, maxCost: number, budget: number): ProblemSpec {
  const changes = [];
  for (let i = 0; i < n; i++) {
    const deps: string[] = [];
    for (let j = 0; j < i; j++) {
      if (rand() < 0.15) deps.push(`c${j}`);
    }
    changes.push({ id: `c${i}`, cost: 1 + Math.floor(rand() * maxCost), deps });
  }
  return { budget, changes };
}

test("validateSpec 拒绝非法输入", () => {
  assert.ok(validateSpec({ budget: 0, changes: [{ id: "a", cost: 1 }] }).length > 0);
  assert.ok(
    validateSpec({ budget: 5, changes: [{ id: "a", cost: 1 }, { id: "a", cost: 2 }] }).length > 0,
  );
  assert.ok(
    validateSpec({ budget: 5, changes: [{ id: "a", cost: 1, deps: ["ghost"] }] }).length > 0,
  );
  assert.ok(
    validateSpec({ budget: 5, changes: [{ id: "a", cost: 1, deps: ["a"] }] }).length > 0,
  );
  const tooMany = Array.from({ length: 19 }, (_, i) => ({ id: `c${i}`, cost: 1 }));
  assert.ok(validateSpec({ budget: 5, changes: tooMany }).length > 0);
  assert.equal(validateSpec({ budget: 5, changes: [{ id: "a", cost: 1 }] }).length, 0);
});

test("依赖闭包支持传递依赖与环形依赖", () => {
  const spec: ProblemSpec = {
    budget: 100,
    changes: [
      { id: "a", cost: 1, deps: ["b"] },
      { id: "b", cost: 2, deps: ["c"] },
      { id: "c", cost: 4 },
      { id: "x", cost: 1, deps: ["y"] },
      { id: "y", cost: 1, deps: ["x"] },
    ],
  };
  const model = buildModel(spec);
  assert.equal(model.closureCost[0], 7);
  assert.equal(model.closureCost[1], 6);
  assert.equal(model.closureCost[2], 4);
  assert.equal(model.closureMask[3], model.closureMask[4]);
  assert.equal(model.closureCost[3], 2);
});

test("合法组合枚举遵守依赖闭包与预算", () => {
  const spec: ProblemSpec = {
    budget: 6,
    changes: [
      { id: "a", cost: 2, deps: ["b"] },
      { id: "b", cost: 2 },
      { id: "c", cost: 3 },
    ],
  };
  const model = buildModel(spec);
  const combos = enumerateLegalCombos(model, spec.budget);
  const sets = combos.map((c) => c.changes.slice().sort().join("+")).sort();
  assert.deepEqual(sets, ["a+b", "b+c"]);
});

test("可达性分析与未覆盖原因", () => {
  const spec: ProblemSpec = {
    budget: 5,
    changes: [
      { id: "a", cost: 2 },
      { id: "b", cost: 2 },
      { id: "big", cost: 6 },
      { id: "d", cost: 4, deps: ["e"] },
      { id: "e", cost: 4 },
    ],
  };
  const model = buildModel(spec);
  const reach = analyzeReachability(model, spec.budget);
  assert.deepEqual(reach.unselectable, ["big", "d"]);
  assert.equal(reach.reachableCount, 1);
  const ab = reach.uncovered.find((p) => p.a === "a" && p.b === "big");
  assert.match(ab!.reason, /自身依赖闭包成本 6 已超预算 5/);
  const ae = reach.uncovered.find((p) => p.a === "a" && p.b === "e");
  assert.match(ae!.reason, /联合依赖闭包/);
});

test("精确解与独立暴力解一致（小规模随机）", () => {
  const rand = mulberry32(42);
  for (let t = 0; t < 30; t++) {
    const spec = randomSpec(rand, 7, 5, 8);
    const model = buildModel(spec);
    const combos = enumerateLegalCombos(model, spec.budget);
    const reach = analyzeReachability(model, spec.budget);
    const exact = solveExact(combos, reach.reachableMask);
    const expected = bruteForceCover(combos, reach.reachableMask);
    assert.equal(exact.totalCost, expected, `seed 42 第 ${t} 例不一致`);
  }
});

test("贪心成本不低于精确解，且两者都覆盖全部可达交互", () => {
  const rand = mulberry32(7);
  for (let t = 0; t < 30; t++) {
    const spec = randomSpec(rand, 9, 6, 12);
    const model = buildModel(spec);
    const combos = enumerateLegalCombos(model, spec.budget);
    const reach = analyzeReachability(model, spec.budget);
    const exact = solveExact(combos, reach.reachableMask);
    const greedy = solveGreedy(combos, reach.reachableMask);
    assert.ok(greedy.totalCost >= exact.totalCost);
    let covered = 0n;
    for (const i of exact.comboIndexes) covered |= combos[i].pairMask;
    assert.equal(covered & reach.reachableMask, reach.reachableMask);
    covered = 0n;
    for (const i of greedy.comboIndexes) covered |= combos[i].pairMask;
    assert.equal(covered & reach.reachableMask, reach.reachableMask);
  }
});

test("demo 示例：精确解优于贪心，未覆盖项有原因", () => {
  const spec = JSON.parse(
    readFileSync(new URL("../examples/demo.json", import.meta.url), "utf8"),
  ) as ProblemSpec;
  const analysis = plan(spec);
  assert.equal(analysis.reachablePairs + analysis.uncovered.length, analysis.totalPairs);
  assert.ok(analysis.exact.totalCost <= analysis.greedy.totalCost);
  assert.ok(analysis.gap.absolute >= 0);
  assert.ok(analysis.uncovered.length > 0);
  for (const pair of analysis.uncovered) {
    assert.ok(pair.reason.length > 0);
  }
});

test("预算过小导致全部不可达时成本为零", () => {
  const analysis = plan({
    budget: 1,
    changes: [
      { id: "a", cost: 2 },
      { id: "b", cost: 3 },
    ],
  });
  assert.equal(analysis.reachablePairs, 0);
  assert.equal(analysis.exact.totalCost, 0);
  assert.equal(analysis.greedy.totalCost, 0);
  assert.equal(analysis.uncovered.length, 1);
});

test("十八个改动可在合理时间内求解", () => {
  const rand = mulberry32(2026);
  const spec = randomSpec(rand, 18, 6, 14);
  const start = Date.now();
  const analysis = plan(spec);
  const elapsed = Date.now() - start;
  assert.ok(elapsed < 20000, `耗时 ${elapsed}ms 超限`);
  assert.ok(analysis.exact.totalCost <= analysis.greedy.totalCost);
});

test("pairMaskOf 覆盖全部成员对", () => {
  const mask = (1n << 0n) | (1n << 2n) | (1n << 3n);
  const pairs = pairMaskOf(mask, 5);
  const expectBits = [0 * 5 + 2, 0 * 5 + 3, 2 * 5 + 3];
  for (const bit of expectBits) {
    assert.ok((pairs & (1n << BigInt(bit))) !== 0n);
  }
  assert.equal(Number(pairs.toString(2).replace(/0/g, "").length), 3);
});
