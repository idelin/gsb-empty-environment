import { conflictWitness, isLegal, maskCost, popcount } from "./model";
import type { ExactResult, Model, Solution, UncoveredPair } from "./types";

/**
 * 精确解：枚举全部 2^n 个子集（n <= 18，最多 262144 个），
 * 按字典序优化：先最大化覆盖的二阶交互数，再最小化总成本。
 * 同时统计每个改动对在合法方案中的最优覆盖，用于解释未覆盖原因。
 */
export function solveExact(model: Model, budget: number): ExactResult {
  const n = model.n;
  const size = 1 << n;

  // costOf[mask]：子集总成本，按低位递推。
  const costOf = new Float64Array(size);
  for (let mask = 1; mask < size; mask++) {
    const lsb = mask & -mask;
    const i = 31 - Math.clz32(lsb);
    costOf[mask] = costOf[mask ^ lsb] + model.costs[i];
  }

  const pairCount = n * n;
  // bestCovWith[p]：包含改动对 p 的合法方案中，最大的交互覆盖数（-1 表示该对不可达）。
  const bestCovWith = new Int32Array(pairCount).fill(-1);
  // bestCostWith[p]：达到 bestCovWith[p] 的方案中的最小成本。
  const bestCostWith = new Float64Array(pairCount).fill(Infinity);

  let best: Solution = { mask: 0, cost: 0, coveredPairs: 0 };

  for (let mask = 0; mask < size; mask++) {
    const cost = costOf[mask];
    if (!isLegal(model, mask, cost, budget)) continue;

    const k = popcount(mask);
    const covered = (k * (k - 1)) / 2;
    if (covered > best.coveredPairs || (covered === best.coveredPairs && cost < best.cost)) {
      best = { mask, cost, coveredPairs: covered };
    }

    for (let i = 0; i < n; i++) {
      if (!(mask & (1 << i))) continue;
      for (let j = i + 1; j < n; j++) {
        if (!(mask & (1 << j))) continue;
        const p = i * n + j;
        if (covered > bestCovWith[p] || (covered === bestCovWith[p] && cost < bestCostWith[p])) {
          bestCovWith[p] = covered;
          bestCostWith[p] = cost;
        }
      }
    }
  }

  let reachablePairs = 0;
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (bestCovWith[i * n + j] >= 0) reachablePairs++;
    }
  }

  const uncovered = explainUncovered(model, budget, best, bestCovWith, bestCostWith);
  return {
    optimal: best,
    reachablePairs,
    totalPairs: (n * (n - 1)) / 2,
    uncovered,
  };
}

function explainUncovered(
  model: Model,
  budget: number,
  best: Solution,
  bestCovWith: Int32Array,
  bestCostWith: Float64Array,
): UncoveredPair[] {
  const n = model.n;
  const result: UncoveredPair[] = [];
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (best.mask & (1 << i) && best.mask & (1 << j)) continue; // 已覆盖
      const a = model.ids[i];
      const b = model.ids[j];
      const p = i * n + j;
      if (bestCovWith[p] < 0) {
        // 不可达：依赖闭包是最小超集，其不合法性即不可达的根因。
        const closure = model.closureMask[i] | model.closureMask[j];
        const witness = conflictWitness(model, closure);
        if (witness) {
          const [x, y] = witness;
          result.push({
            a,
            b,
            reason: {
              kind: "conflict",
              a,
              b,
              detail: `依赖闭包内存在冲突：${model.ids[x]} ↔ ${model.ids[y]}`,
            },
          });
        } else {
          const minCost = maskCost(closure, model.costs);
          result.push({
            a,
            b,
            reason: { kind: "budget", a, b, minCost, budget },
          });
        }
      } else if (bestCovWith[p] < best.coveredPairs) {
        result.push({
          a,
          b,
          reason: {
            kind: "sacrifice",
            a,
            b,
            bestWithPair: bestCovWith[p],
            optimal: best.coveredPairs,
          },
        });
      } else {
        result.push({
          a,
          b,
          reason: {
            kind: "costlier",
            a,
            b,
            costWithPair: bestCostWith[p],
            optimalCost: best.cost,
          },
        });
      }
    }
  }
  return result;
}
