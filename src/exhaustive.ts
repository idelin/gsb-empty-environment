/**
 * Exhaustive enumeration for small instances. This is the independent
 * cross-check of the branch-and-bound optimum: it shares no search code
 * with the B&B solver, only the (separately reviewable) error/constraint
 * evaluators.
 */
import { cmp } from './rational.ts';
import type { Rational } from './rational.ts';
import type { Instance } from './model.ts';
import { evaluateError, checkConstraints, selectionCost } from './error.ts';
import type { Selection, ErrorReport } from './error.ts';

export interface ExhaustiveResult {
  readonly totalCombinations: number;
  readonly validCombinations: number; // constraints satisfied
  readonly feasibleCombinations: number; // valid + within error budget
  readonly best: { selection: Selection; cost: Rational; error: ErrorReport } | null;
  readonly truncated: boolean; // true if combination cap stopped the run
}

export function exhaustiveSolve(
  inst: Instance,
  maxCombinations = 1_000_000,
): ExhaustiveResult {
  const n = inst.components.length;
  let total = 0;
  let valid = 0;
  let feasible = 0;
  let best: ExhaustiveResult['best'] = null;
  let truncated = false;

  const idx = new Array<number>(n).fill(0);
  const walk = (k: number): void => {
    if (truncated) return;
    if (k === n) {
      total++;
      if (total > maxCombinations) {
        truncated = true;
        total--;
        return;
      }
      const selection = new Map<string, string>(
        inst.components.map((c, ci) => [c.id, c.tiers[idx[ci]].id]),
      );
      if (checkConstraints(inst, selection).length > 0) return;
      valid++;
      const error = evaluateError(inst, selection);
      if (!error.feasible) return;
      feasible++;
      const cost = selectionCost(inst, selection);
      if (best === null || cmp(cost, best.cost) < 0) best = { selection, cost, error };
      return;
    }
    for (let t = 0; t < inst.components[k].tiers.length; t++) {
      idx[k] = t;
      walk(k + 1);
    }
  };
  walk(0);
  return { totalCombinations: total, validCombinations: valid, feasibleCombinations: feasible, best, truncated };
}
