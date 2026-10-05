import { BoxAllocation, BoxType, Problem, Solution, Store, StorePlan } from "./types";

export const MAX_BOX_TYPES = 8;
export const MAX_STORES = 5;
const MAX_DEMAND = 1000000;

function assertInteger(value: number, name: string): void {
  if (typeof value !== "number" || !isFinite(value) || Math.floor(value) !== value) {
    throw new Error(`${name} 必须为整数，得到: ${value}`);
  }
}

/** 校验输入并施加规模约束（最多 8 种箱型、5 家门店） */
export function validateProblem(problem: Problem): void {
  if (!Array.isArray(problem.boxTypes) || problem.boxTypes.length === 0) {
    throw new Error("至少需要 1 种箱型");
  }
  if (problem.boxTypes.length > MAX_BOX_TYPES) {
    throw new Error(`箱型最多 ${MAX_BOX_TYPES} 种，得到 ${problem.boxTypes.length}`);
  }
  if (!Array.isArray(problem.stores) || problem.stores.length === 0) {
    throw new Error("至少需要 1 家门店");
  }
  if (problem.stores.length > MAX_STORES) {
    throw new Error(`门店最多 ${MAX_STORES} 家，得到 ${problem.stores.length}`);
  }
  const boxIds = new Set<string>();
  for (const b of problem.boxTypes) {
    if (!b.id) throw new Error("箱型缺少 id");
    if (boxIds.has(b.id)) throw new Error(`箱型 id 重复: ${b.id}`);
    boxIds.add(b.id);
    assertInteger(b.capacity, `箱型 ${b.id} 的 capacity`);
    if (b.capacity <= 0) throw new Error(`箱型 ${b.id} 的 capacity 必须为正`);
    assertInteger(b.unitCost, `箱型 ${b.id} 的 unitCost`);
    if (b.unitCost < 0) throw new Error(`箱型 ${b.id} 的 unitCost 不能为负`);
  }
  const storeIds = new Set<string>();
  for (const s of problem.stores) {
    if (!s.id) throw new Error("门店缺少 id");
    if (storeIds.has(s.id)) throw new Error(`门店 id 重复: ${s.id}`);
    storeIds.add(s.id);
    assertInteger(s.demand, `门店 ${s.id} 的 demand`);
    if (s.demand < 0 || s.demand > MAX_DEMAND) {
      throw new Error(`门店 ${s.id} 的 demand 需在 [0, ${MAX_DEMAND}] 内`);
    }
    assertInteger(s.shortagePenalty, `门店 ${s.id} 的 shortagePenalty`);
    if (s.shortagePenalty < 0) throw new Error(`门店 ${s.id} 的 shortagePenalty 不能为负`);
    if (s.transportCostPerBox !== undefined) {
      assertInteger(s.transportCostPerBox, `门店 ${s.id} 的 transportCostPerBox`);
      if (s.transportCostPerBox < 0) throw new Error(`门店 ${s.id} 的 transportCostPerBox 不能为负`);
    }
  }
  if (problem.transportCostPerBox !== undefined) {
    assertInteger(problem.transportCostPerBox, "transportCostPerBox");
    if (problem.transportCostPerBox < 0) throw new Error("transportCostPerBox 不能为负");
  }
}

/**
 * 单门店子问题（精确求解）：
 *   min  Σ_i (unitCost_i + transport_i) * x_i + penalty * max(0, D - Σ_i cap_i * x_i)
 *   s.t. x_i 为非负整数
 *
 * 箱不可拆分且整箱归属单一门店，门店间无耦合约束，
 * 因此原问题按门店分解，各店独立求解即得全局最优。
 *
 * 方法：完全背包 DP。g(v) = 恰好配送 v 件到该店的最小（采购+运输）成本，
 * v ∈ [0, D + maxCap - 1]（超过该上界的配送必然被某个不超界的方案支配）。
 * 然后枚举 v，加上欠供罚金取最小。
 */
export function solveStore(
  boxTypes: BoxType[],
  store: Store,
  defaultTransportPerBox: number
): StorePlan {
  const demand = store.demand;
  const penalty = store.shortagePenalty;
  const transport = store.transportCostPerBox !== undefined
    ? store.transportCostPerBox
    : defaultTransportPerBox;

  const weights = boxTypes.map((b) => b.unitCost + transport);
  const maxCap = Math.max.apply(null, boxTypes.map((b) => b.capacity));
  const upper = demand + maxCap - 1;

  const INF = Number.MAX_SAFE_INTEGER;
  const cost: number[] = new Array<number>(upper + 1).fill(INF);
  const parent: number[] = new Array<number>(upper + 1).fill(-1); // 选用的箱型下标
  cost[0] = 0;
  for (let v = 1; v <= upper; v++) {
    for (let i = 0; i < boxTypes.length; i++) {
      const cap = boxTypes[i].capacity;
      if (cap <= v && cost[v - cap] !== INF) {
        const cand = cost[v - cap] + weights[i];
        if (cand < cost[v]) {
          cost[v] = cand;
          parent[v] = i;
        }
      }
    }
  }

  // 枚举实际配送量 v：总成本 = 配送成本 + 欠供罚金
  let bestV = 0;
  let bestTotal = penalty * demand; // v = 0
  for (let v = 0; v <= upper; v++) {
    if (cost[v] === INF) continue;
    const shortage = demand - v > 0 ? demand - v : 0;
    const total = cost[v] + penalty * shortage;
    const bestShortage = demand - bestV > 0 ? demand - bestV : 0;
    // 平局时偏好欠供更少、再偏好配送量更小（剩余库存更少）
    if (
      total < bestTotal ||
      (total === bestTotal &&
        (shortage < bestShortage || (shortage === bestShortage && v < bestV)))
    ) {
      bestTotal = total;
      bestV = v;
    }
  }

  // 回溯箱型数量
  const counts = new Array<number>(boxTypes.length).fill(0);
  let v = bestV;
  while (v > 0) {
    const i = parent[v];
    counts[i]++;
    v -= boxTypes[i].capacity;
  }

  const boxes: BoxAllocation[] = [];
  let procurement = 0;
  let boxCount = 0;
  for (let i = 0; i < boxTypes.length; i++) {
    if (counts[i] > 0) {
      boxes.push({ boxTypeId: boxTypes[i].id, count: counts[i], capacity: boxTypes[i].capacity });
      procurement += counts[i] * boxTypes[i].unitCost;
      boxCount += counts[i];
    }
  }
  const shortage = demand > bestV ? demand - bestV : 0;
  return {
    storeId: store.id,
    boxes,
    unitsSupplied: bestV,
    shortage,
    leftover: bestV > demand ? bestV - demand : 0,
    procurementCost: procurement,
    transportCost: boxCount * transport,
    shortageCost: penalty * shortage,
    totalCost: bestTotal,
  };
}

/** 求解整个补货规划问题 */
export function solve(problem: Problem): Solution {
  validateProblem(problem);
  const defaultTransport = problem.transportCostPerBox !== undefined ? problem.transportCostPerBox : 0;

  const storePlans = problem.stores.map((s) =>
    solveStore(problem.boxTypes, s, defaultTransport)
  );

  const totalBoxCounts = new Array<number>(problem.boxTypes.length).fill(0);
  for (const plan of storePlans) {
    for (const alloc of plan.boxes) {
      const idx = problem.boxTypes.findIndex((b) => b.id === alloc.boxTypeId);
      totalBoxCounts[idx] += alloc.count;
    }
  }
  const totalBoxes: BoxAllocation[] = [];
  for (let i = 0; i < problem.boxTypes.length; i++) {
    if (totalBoxCounts[i] > 0) {
      totalBoxes.push({
        boxTypeId: problem.boxTypes[i].id,
        count: totalBoxCounts[i],
        capacity: problem.boxTypes[i].capacity,
      });
    }
  }

  const sum = (f: (p: StorePlan) => number) =>
    storePlans.reduce((acc, p) => acc + f(p), 0);

  return {
    storePlans,
    totalBoxes,
    totalProcurementCost: sum((p) => p.procurementCost),
    totalTransportCost: sum((p) => p.transportCost),
    totalShortageCost: sum((p) => p.shortageCost),
    totalCost: sum((p) => p.totalCost),
    totalLeftover: sum((p) => p.leftover),
    totalShortage: sum((p) => p.shortage),
  };
}
