import {
  BoxType,
  Plan,
  PlanInput,
  Store,
  StorePlan,
} from "./types";

export const MAX_BOX_TYPES = 8;
export const MAX_STORES = 5;

/** 校验输入规模与取值约束。 */
export function validateInput(input: PlanInput): void {
  if (!Array.isArray(input.boxTypes) || input.boxTypes.length === 0) {
    throw new Error("至少需要 1 种箱型");
  }
  if (input.boxTypes.length > MAX_BOX_TYPES) {
    throw new Error(`箱型最多 ${MAX_BOX_TYPES} 种，当前 ${input.boxTypes.length} 种`);
  }
  if (!Array.isArray(input.stores) || input.stores.length === 0) {
    throw new Error("至少需要 1 家门店");
  }
  if (input.stores.length > MAX_STORES) {
    throw new Error(`门店最多 ${MAX_STORES} 家，当前 ${input.stores.length} 家`);
  }

  const boxIds = new Set<string>();
  for (const b of input.boxTypes) {
    if (!b.id) throw new Error("箱型缺少 id");
    if (boxIds.has(b.id)) throw new Error(`箱型 id 重复: ${b.id}`);
    boxIds.add(b.id);
    if (!Number.isInteger(b.capacity) || b.capacity < 1) {
      throw new Error(`箱型 ${b.id} 的 capacity 必须是 >= 1 的整数`);
    }
    for (const [name, v] of [
      ["purchaseCost", b.purchaseCost],
      ["transportCost", b.transportCost],
    ] as const) {
      if (typeof v !== "number" || !isFinite(v) || v < 0) {
        throw new Error(`箱型 ${b.id} 的 ${name} 必须是非负有限数`);
      }
    }
    if (b.transportCostByStore) {
      for (const [sid, v] of Object.entries(b.transportCostByStore)) {
        if (typeof v !== "number" || !isFinite(v) || v < 0) {
          throw new Error(`箱型 ${b.id} 对门店 ${sid} 的运输成本必须是非负有限数`);
        }
      }
    }
  }

  const storeIds = new Set<string>();
  for (const s of input.stores) {
    if (!s.id) throw new Error("门店缺少 id");
    if (storeIds.has(s.id)) throw new Error(`门店 id 重复: ${s.id}`);
    storeIds.add(s.id);
    if (!Number.isInteger(s.demand) || s.demand < 0) {
      throw new Error(`门店 ${s.id} 的 demand 必须是非负整数`);
    }
    if (
      typeof s.shortagePenaltyPerUnit !== "number" ||
      !isFinite(s.shortagePenaltyPerUnit) ||
      s.shortagePenaltyPerUnit < 0
    ) {
      throw new Error(`门店 ${s.id} 的 shortagePenaltyPerUnit 必须是非负有限数`);
    }
    if (
      s.maxShortage !== undefined &&
      (!Number.isInteger(s.maxShortage) || s.maxShortage < 0)
    ) {
      throw new Error(`门店 ${s.id} 的 maxShortage 必须是非负整数`);
    }
  }
}

function transportCostOf(box: BoxType, storeId: string): number {
  return box.transportCostByStore?.[storeId] ?? box.transportCost;
}

interface Candidate {
  delivered: number;
  boxCost: number;
  shortage: number;
  leftover: number;
  totalCost: number;
  boxCount: number;
}

/**
 * 单门店精确求解（无界背包 DP + 欠供罚金权衡）。
 *
 * 由于箱不可拆、且禁止跨店无偿流转，各门店问题相互独立：
 * 选整数 x_t >= 0 使  sum_t (采购_t + 运输_t) * x_t + 罚金 * max(0, D - sum_t a_t x_t)  最小。
 *
 * 送达量上界取 D + maxCapacity：若送达 >= D + maxCapacity，去掉任意一箱仍满足需求
 * 且成本不增，因此最优解必在该上界之内。
 */
export function planStore(boxTypes: BoxType[], store: Store): StorePlan {
  const unitCost = boxTypes.map((b) => b.purchaseCost + transportCostOf(b, store.id));
  const maxCap = Math.max(...boxTypes.map((b) => b.capacity));
  const upper = store.demand + maxCap;

  // dp[u] = 恰好送达 u 件的最小箱成本；parent[u] = 达到 u 时最后放入的箱型下标
  const dp = new Array<number>(upper + 1).fill(Infinity);
  const parent = new Array<number>(upper + 1).fill(-1);
  dp[0] = 0;
  for (let u = 1; u <= upper; u++) {
    for (let t = 0; t < boxTypes.length; t++) {
      const a = boxTypes[t].capacity;
      if (a > u) continue;
      const cand = dp[u - a] + unitCost[t];
      if (cand < dp[u]) {
        dp[u] = cand;
        parent[u] = t;
      }
    }
  }

  // 在所有可达送达量中挑选 总成本 = 箱成本 + 欠供罚金 最小者。
  // 平局时依次偏好：剩余库存更少、欠供更少、箱数更少。
  let best: Candidate | null = null;
  for (let u = 0; u <= upper; u++) {
    if (!isFinite(dp[u])) continue;
    const shortage = Math.max(0, store.demand - u);
    if (store.maxShortage !== undefined && shortage > store.maxShortage) continue;
    const leftover = Math.max(0, u - store.demand);
    const cand: Candidate = {
      delivered: u,
      boxCost: dp[u],
      shortage,
      leftover,
      totalCost: dp[u] + store.shortagePenaltyPerUnit * shortage,
      boxCount: 0,
    };
    if (
      best === null ||
      cand.totalCost < best.totalCost ||
      (cand.totalCost === best.totalCost &&
        (cand.leftover < best.leftover ||
          (cand.leftover === best.leftover && cand.shortage < best.shortage)))
    ) {
      best = cand;
    }
  }
  if (best === null) {
    // 仅在 maxShortage 限制过严且箱型无法组合出足够送达量时发生
    throw new Error(`门店 ${store.id} 无可行解（maxShortage 约束过严）`);
  }

  // 回溯箱型数量
  const counts = new Array<number>(boxTypes.length).fill(0);
  let u = best.delivered;
  while (u > 0) {
    const t = parent[u];
    counts[t]++;
    u -= boxTypes[t].capacity;
    best.boxCount++;
  }

  const boxes: Record<string, number> = {};
  let purchaseCost = 0;
  let transportCost = 0;
  boxTypes.forEach((b, t) => {
    if (counts[t] > 0) boxes[b.id] = counts[t];
    purchaseCost += b.purchaseCost * counts[t];
    transportCost += transportCostOf(b, store.id) * counts[t];
  });

  return {
    storeId: store.id,
    boxes,
    totalBoxes: best.boxCount,
    delivered: best.delivered,
    demand: store.demand,
    shortage: best.shortage,
    leftover: best.leftover,
    purchaseCost,
    transportCost,
    shortagePenalty: store.shortagePenaltyPerUnit * best.shortage,
    totalCost: purchaseCost + transportCost + store.shortagePenaltyPerUnit * best.shortage,
  };
}

/**
 * 全局规划：逐门店独立求最优后汇总。
 * 整箱直接配送到店，剩余库存留在原门店，不做跨店拆分流转。
 */
export function planReplenishment(input: PlanInput): Plan {
  validateInput(input);
  const stores = input.stores.map((s) => planStore(input.boxTypes, s));
  const sum = (f: (p: StorePlan) => number) => stores.reduce((acc, p) => acc + f(p), 0);
  return {
    stores,
    totalLeftover: sum((p) => p.leftover),
    totalDelivered: sum((p) => p.delivered),
    totalShortage: sum((p) => p.shortage),
    purchaseCost: sum((p) => p.purchaseCost),
    transportCost: sum((p) => p.transportCost),
    shortagePenalty: sum((p) => p.shortagePenalty),
    totalCost: sum((p) => p.totalCost),
  };
}
