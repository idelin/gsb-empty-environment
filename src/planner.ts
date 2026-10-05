import type { Analysis, ComboInfo, ProblemSpec, SolutionInfo } from "./types.ts";
import {
  analyzeReachability,
  buildModel,
  enumerateLegalCombos,
  popcount,
  validateSpec,
} from "./core.ts";
import { solveExact, solveGreedy } from "./solvers.ts";

export function plan(spec: ProblemSpec): Analysis {
  const errors = validateSpec(spec);
  if (errors.length > 0) {
    throw new Error(`输入校验失败:\n${errors.map((e) => `  - ${e}`).join("\n")}`);
  }

  const model = buildModel(spec);
  const n = model.ids.length;
  const combos = enumerateLegalCombos(model, spec.budget);
  const reach = analyzeReachability(model, spec.budget);

  const exact = solveExact(combos, reach.reachableMask);
  const greedy = solveGreedy(combos, reach.reachableMask);

  const toInfo = (comboIndexes: number[], totalCost: number): SolutionInfo => {
    const infos: ComboInfo[] = comboIndexes.map((i) => ({
      changes: combos[i].changes,
      cost: combos[i].cost,
      pairs: popcount(combos[i].pairMask & reach.reachableMask),
    }));
    infos.sort((a, b) => b.pairs - a.pairs || a.cost - b.cost);
    return { combos: infos, totalCost, coveredPairs: reach.reachableCount };
  };

  const exactInfo = toInfo(exact.comboIndexes, exact.totalCost);
  const greedyInfo = toInfo(greedy.comboIndexes, greedy.totalCost);
  const absolute = greedy.totalCost - exact.totalCost;

  return {
    budget: spec.budget,
    changeCount: n,
    legalComboCount: combos.length,
    totalPairs: (n * (n - 1)) / 2,
    reachablePairs: reach.reachableCount,
    exact: exactInfo,
    greedy: greedyInfo,
    gap: {
      absolute,
      percent: exact.totalCost > 0 ? (absolute / exact.totalCost) * 100 : 0,
    },
    uncovered: reach.uncovered,
    unselectable: reach.unselectable,
  };
}
