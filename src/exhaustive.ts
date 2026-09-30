/**
 * Brute-force enumeration for small instances: the ground-truth cross-check
 * for the branch & bound solver. Refuses to run above a combination cap so
 * it is never silently slow.
 */

import * as R from "./rational.ts";
import { analyze, refIndex, totalCost } from "./model.ts";
import type { Assignment, Problem } from "./model.ts";

export interface ExhaustiveResult {
  status: "exhausted-all" | "too-large";
  combinationsChecked: number;
  feasibleCount: number;
  bestCost: R.Rational | null;
  bestAssignment: Map<number, number> | null;
}

export function exhaustive(problem: Problem, maxCombinations = 1_000_000): ExhaustiveResult {
  const n = problem.components.length;
  let total = 1;
  for (const c of problem.components) {
    total *= c.grades.length;
    if (total > maxCombinations) {
      return { status: "too-large", combinationsChecked: 0, feasibleCount: 0, bestCost: null, bestAssignment: null };
    }
  }

  const excl = problem.exclusions.map((e) => ({ a: refIndex(problem, e.a), b: refIndex(problem, e.b) }));
  const deps = problem.dependencies.map((d) => ({ if: refIndex(problem, d.if), req: refIndex(problem, d.requires) }));

  const choice = new Array<number>(n).fill(0);
  let checked = 0;
  let feasibleCount = 0;
  let bestCost: R.Rational | null = null;
  let bestAssignment: Map<number, number> | null = null;

  const violates = (): boolean => {
    for (const { a, b } of excl) {
      if (choice[a.ci] === a.gi && choice[b.ci] === b.gi) return true;
    }
    for (const { if: cond, req } of deps) {
      if (choice[cond.ci] === cond.gi && choice[req.ci] !== req.gi) return true;
    }
    return false;
  };

  for (let k = 0; k < total; k++) {
    // odometer
    let rem = k;
    for (let ci = 0; ci < n; ci++) {
      const len = problem.components[ci].grades.length;
      choice[ci] = rem % len;
      rem = Math.floor(rem / len);
    }
    checked++;
    if (violates()) continue;
    const assignment: Assignment = new Map(choice.map((gi, ci) => [ci, gi]));
    if (!analyze(problem, assignment).feasible) continue;
    feasibleCount++;
    const cost = totalCost(problem, assignment);
    if (bestCost === null || R.cmp(cost, bestCost) < 0) {
      bestCost = cost;
      bestAssignment = new Map(assignment);
    }
  }
  return { status: "exhausted-all", combinationsChecked: checked, feasibleCount, bestCost, bestAssignment };
}
