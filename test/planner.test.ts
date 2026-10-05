import { describe, expect, test } from "bun:test";
import { solveExact } from "../src/exact";
import { solveGreedy } from "../src/greedy";
import { buildModel, isLegal, maskCost, MAX_CHANGES, popcount } from "../src/model";
import type { Instance } from "../src/types";

/** 可复现的伪随机数生成器。 */
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

function randomInstance(rand: () => number, n: number): Instance {
  const changes = [];
  for (let i = 0; i < n; i++) {
    const requires: string[] = [];
    const conflicts: string[] = [];
    for (let j = 0; j < n; j++) {
      if (j === i) continue;
      const r = rand();
      if (r < 0.08) requires.push(`C${j}`);
      else if (r < 0.16) conflicts.push(`C${j}`);
    }
    changes.push({
      id: `C${i}`,
      cost: 1 + Math.floor(rand() * 8),
      requires,
      conflicts,
    });
  }
  const total = changes.reduce((s, c) => s + c.cost, 0);
  const budget = Math.floor(total * (0.3 + rand() * 0.5));
  return { budget, changes };
}

/**
 * 独立实现的暴力对照：不依赖 src/model 的闭包逻辑，
 * 直接按题意枚举子集验证最优覆盖与成本。
 */
function bruteForceReference(instance: Instance): { covered: number; cost: number; reachable: number } {
  const n = instance.changes.length;
  const req = instance.changes.map((c) => (c.requires ?? []).map((r) => instance.changes.findIndex((x) => x.id === r)));
  const conf = instance.changes.map((c) => (c.conflicts ?? []).map((r) => instance.changes.findIndex((x) => x.id === r)));

  const closureOf = (start: number[]): Set<number> => {
    const set = new Set(start);
    let grew = true;
    while (grew) {
      grew = false;
      for (const i of [...set]) {
        for (const j of req[i]) {
          if (!set.has(j)) {
            set.add(j);
            grew = true;
          }
        }
      }
    }
    return set;
  };

  const legal = (set: Set<number>): boolean => {
    let cost = 0;
    for (const i of set) {
      cost += instance.changes[i].cost;
      for (const j of conf[i]) if (set.has(j)) return false;
    }
    return cost <= instance.budget;
  };

  let bestCovered = 0;
  let bestCost = 0;
  const reachable = new Set<string>();
  for (let mask = 0; mask < 1 << n; mask++) {
    const seedSet: number[] = [];
    for (let i = 0; i < n; i++) if (mask & (1 << i)) seedSet.push(i);
    const set = closureOf(seedSet);
    // 只统计"恰好等于自身闭包"的掩码，避免重复评估同一方案。
    if (set.size !== seedSet.length) continue;
    if (!legal(set)) continue;
    const k = set.size;
    const covered = (k * (k - 1)) / 2;
    let cost = 0;
    for (const i of set) cost += instance.changes[i].cost;
    if (covered > bestCovered || (covered === bestCovered && cost < bestCost)) {
      bestCovered = covered;
      bestCost = cost;
    }
    const arr = [...set];
    for (let x = 0; x < arr.length; x++) {
      for (let y = x + 1; y < arr.length; y++) reachable.add(`${arr[x]},${arr[y]}`);
    }
  }
  return { covered: bestCovered, cost: bestCost, reachable: reachable.size };
}

describe("精确解 vs 独立暴力对照", () => {
  for (let seed = 1; seed <= 40; seed++) {
    test(`随机实例 seed=${seed}`, () => {
      const rand = mulberry32(seed);
      const n = 2 + Math.floor(rand() * 9); // 2..10 个改动
      const instance = randomInstance(rand, n);
      const model = buildModel(instance);
      const exact = solveExact(model, instance.budget);
      const ref = bruteForceReference(instance);
      expect(exact.optimal.coveredPairs).toBe(ref.covered);
      expect(exact.optimal.cost).toBe(ref.cost);
      expect(exact.reachablePairs).toBe(ref.reachable);
    });
  }
});

describe("精确解不劣于贪心", () => {
  for (let seed = 100; seed < 130; seed++) {
    test(`随机实例 seed=${seed}`, () => {
      const rand = mulberry32(seed);
      const n = 2 + Math.floor(rand() * 11); // 2..12 个改动
      const instance = randomInstance(rand, n);
      const model = buildModel(instance);
      const exact = solveExact(model, instance.budget);
      const greedy = solveGreedy(model, instance.budget);
      // 贪心解自身必须合法。
      expect(isLegal(model, greedy.mask, greedy.cost, instance.budget)).toBe(true);
      expect(greedy.coveredPairs).toBe((popcount(greedy.mask) * (popcount(greedy.mask) - 1)) / 2);
      // 字典序：精确解覆盖更多，或覆盖相同但成本更低。
      const exactBetter =
        exact.optimal.coveredPairs > greedy.coveredPairs ||
        (exact.optimal.coveredPairs === greedy.coveredPairs && exact.optimal.cost <= greedy.cost);
      expect(exactBetter).toBe(true);
    });
  }
});

describe("输入校验", () => {
  test("超过 18 个改动报错", () => {
    const changes = Array.from({ length: MAX_CHANGES + 1 }, (_, i) => ({ id: `C${i}`, cost: 1 }));
    expect(() => buildModel({ budget: 10, changes })).toThrow(/超过上限/);
  });
  test("未知依赖引用报错", () => {
    expect(() => buildModel({ budget: 10, changes: [{ id: "A", cost: 1, requires: ["ZZZ"] }] })).toThrow(/未知改动/);
  });
  test("非法成本报错", () => {
    expect(() => buildModel({ budget: 10, changes: [{ id: "A", cost: -1 }] })).toThrow(/成本/);
  });
  test("依赖环合法且同进同出", () => {
    const instance: Instance = {
      budget: 10,
      changes: [
        { id: "A", cost: 1, requires: ["B"] },
        { id: "B", cost: 1, requires: ["A"] },
        { id: "C", cost: 1 },
      ],
    };
    const model = buildModel(instance);
    const exact = solveExact(model, instance.budget);
    // 三个改动可同时选择，覆盖 3 对。
    expect(exact.optimal.coveredPairs).toBe(3);
    expect(maskCost(exact.optimal.mask, model.costs)).toBe(3);
  });
});

describe("未覆盖原因", () => {
  test("冲突与预算原因分类正确", () => {
    const instance: Instance = {
      budget: 5,
      changes: [
        { id: "A", cost: 2 },
        { id: "B", cost: 2, conflicts: ["A"] },
        { id: "C", cost: 4 },
        { id: "D", cost: 4 },
      ],
    };
    const model = buildModel(instance);
    const exact = solveExact(model, instance.budget);
    const byPair = new Map(exact.uncovered.map((u) => [`${u.a},${u.b}`, u.reason.kind]));
    expect(byPair.get("A,B")).toBe("conflict");
    expect(byPair.get("C,D")).toBe("budget");
  });
});
