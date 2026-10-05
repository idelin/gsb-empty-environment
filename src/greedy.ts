import { conflictWitness, maskCost, popcount } from "./model";
import type { Model, Solution } from "./types";

/**
 * 贪心对照：从空集出发，每轮加入"新增覆盖最多、边际成本最低"的改动
 * （加入时自动带上依赖闭包），直到无法在不破坏约束的前提下继续扩大覆盖。
 */
export function solveGreedy(model: Model, budget: number): Solution {
  let mask = 0;
  let cost = 0;
  let covered = 0;

  for (;;) {
    let bestGain = 0;
    let bestExtra = Infinity;
    let bestMask = -1;
    let bestCost = 0;

    for (let c = 0; c < model.n; c++) {
      if (mask & (1 << c)) continue;
      const next = mask | model.closureMask[c];
      if (next === mask) continue;
      const nextCost = cost + deltaCost(model, next & ~mask);
      if (nextCost > budget) continue;
      if (conflictWitness(model, next)) continue;
      const k = popcount(next);
      const nextCovered = (k * (k - 1)) / 2;
      const gain = nextCovered - covered;
      const extra = nextCost - cost;
      // 首要：新增覆盖最大；次要：边际成本最小；再次：掩码小保证确定性。
      if (gain > bestGain || (gain === bestGain && (extra < bestExtra || (extra === bestExtra && next < bestMask)))) {
        bestGain = gain;
        bestExtra = extra;
        bestMask = next;
        bestCost = nextCost;
      }
    }

    // 空集起步时允许 0 增益（先放入第一个改动）；之后 0 增益只浪费成本，停止。
    if (bestMask < 0 || (bestGain === 0 && mask !== 0)) break;
    mask = bestMask;
    cost = bestCost;
    covered = (popcount(mask) * (popcount(mask) - 1)) / 2;
  }

  return { mask, cost, coveredPairs: covered };
}

function deltaCost(model: Model, addedMask: number): number {
  return maskCost(addedMask, model.costs);
}
