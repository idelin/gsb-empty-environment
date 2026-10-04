import { Problem } from "./types";
import { PlanResult, PlanStep } from "./planner";

/**
 * 贪心基线（仅用于对比演示）：每步选择“目标距离最近”的动作——
 * 即新达成目标数最多、成本最低者。不考虑未来资源约束，
 * 因此会一头撞进死端。生产规划请使用 Planner。
 */
export function greedyPlan(problem: Problem): PlanResult {
  const resourceNames = problem.resources.map((r) => r.name);
  const goalIndex = new Map(problem.goals.map((g, i) => [g.name, i]));
  const goalProfit = problem.goals.map((g) => g.profit);

  let res = problem.resources.map((r) => r.capacity);
  let mask = 0n;
  const steps: PlanStep[] = [];
  const achieved: string[] = [];
  let cost = 0;

  for (;;) {
    let bestAi = -1;
    let bestGained = 0;
    let bestCost = Infinity;
    for (let ai = 0; ai < problem.actions.length; ai++) {
      const a = problem.actions[ai];
      const ok = resourceNames.every(
        (rn, ri) => res[ri] >= (a.consumes[rn] ?? 0),
      );
      if (!ok) continue;
      let gainedMask = 0n;
      for (const g of a.achieves) gainedMask |= 1n << BigInt(goalIndex.get(g)!);
      gainedMask &= ~mask;
      const gained = popcount(gainedMask);
      if (gained === 0) continue;
      // 目标距离贪心：新目标越多越好，并列取成本最低。
      if (gained > bestGained || (gained === bestGained && a.cost < bestCost)) {
        bestAi = ai;
        bestGained = gained;
        bestCost = a.cost;
      }
    }
    if (bestAi < 0) break;
    const a = problem.actions[bestAi];
    res = res.map((v, ri) => v - (a.consumes[resourceNames[ri]] ?? 0));
    const achievedNow = a.achieves.filter(
      (g) => (mask & (1n << BigInt(goalIndex.get(g)!))) === 0n,
    );
    for (const g of a.achieves) mask |= 1n << BigInt(goalIndex.get(g)!);
    achieved.push(...achievedNow);
    cost += a.cost;
    const balance: Record<string, number> = {};
    resourceNames.forEach((rn, ri) => (balance[rn] = res[ri]));
    steps.push({ action: a.name, cost: a.cost, achievedNow, balance });
  }

  let profit = 0;
  for (const g of achieved) profit += goalProfit[goalIndex.get(g)!];
  const finalBalance: Record<string, number> = {};
  resourceNames.forEach((rn, ri) => (finalBalance[rn] = res[ri]));
  return {
    profit,
    cost,
    steps,
    achievedGoals: achieved,
    finalBalance,
    statesExplored: 0,
  };
}

function popcount(x: bigint): number {
  let n = 0;
  while (x > 0n) {
    x &= x - 1n;
    n++;
  }
  return n;
}
