import type { NormalizedProblem } from './model.ts';

export interface TightBounds {
  lo: bigint[];
  hi: bigint[];
}

export interface FeasibilityReport {
  feasible: boolean;
  violations: string[];
  /**
   * 缺额: minimum extra budget that would be needed to make all lower
   * bounds satisfiable (0 when the budget side is fine). Never forced.
   */
  deficit: bigint;
}

const maxB = (a: bigint, b: bigint): bigint => (a > b ? a : b);
const minB = (a: bigint, b: bigint): bigint => (a < b ? a : b);

/**
 * Constraint propagation: tighten per-item integer bounds to a fixpoint
 * using the conservation constraint (sum = total) and every group bound.
 * Overlapping groups are handled jointly here, never group-by-group.
 */
export function tighten(problem: NormalizedProblem): TightBounds {
  const n = problem.items.length;
  const lo = problem.items.map((it) => it.lower);
  const hi = problem.items.map((it) => it.upper);
  const total = problem.total;

  let changed = true;
  let guard = 0;
  const maxPasses = 4 * (n + problem.groups.length) + 4;
  while (changed && guard++ < maxPasses) {
    changed = false;
    // Once any interval is empty the instance is infeasible; further
    // propagation would only produce meaningless runaway numbers.
    let empty = false;
    for (let i = 0; i < n; i++) {
      if (lo[i] > hi[i]) {
        empty = true;
        break;
      }
    }
    if (empty) break;

    // Conservation: x_i = total - sum_{j != i} x_j.
    let sumLo = 0n;
    let sumHi = 0n;
    for (let i = 0; i < n; i++) {
      sumLo += lo[i];
      sumHi += hi[i];
    }
    for (let i = 0; i < n; i++) {
      const newHi = minB(hi[i], total - (sumLo - lo[i]));
      const newLo = maxB(lo[i], total - (sumHi - hi[i]));
      if (newHi !== hi[i]) {
        hi[i] = newHi;
        changed = true;
      }
      if (newLo !== lo[i]) {
        lo[i] = newLo;
        changed = true;
      }
    }

    // Each group: sum of members in [g.lower, g.upper].
    for (const g of problem.groups) {
      let gLo = 0n;
      let gHi = 0n;
      for (const m of g.memberIdx) {
        gLo += lo[m];
        gHi += hi[m];
      }
      for (const m of g.memberIdx) {
        let newLo = lo[m];
        let newHi = hi[m];
        if (g.lower !== null) newLo = maxB(newLo, g.lower - (gHi - hi[m]));
        if (g.upper !== null) newHi = minB(newHi, g.upper - (gLo - lo[m]));
        if (newLo !== lo[m]) {
          lo[m] = newLo;
          changed = true;
        }
        if (newHi !== hi[m]) {
          hi[m] = newHi;
          changed = true;
        }
      }
    }
  }
  return { lo, hi };
}

/**
 * Independent feasibility check with explicit deficit (缺额) reporting.
 * Infeasible inputs are reported, never silently forced into feasibility.
 */
export function checkFeasibility(
  problem: NormalizedProblem,
  bounds: TightBounds,
): FeasibilityReport {
  const { total } = problem;
  const violations: string[] = [];
  let deficit = 0n;

  let sumLo = 0n;
  let sumHi = 0n;
  for (let i = 0; i < problem.items.length; i++) {
    sumLo += problem.items[i].lower;
    sumHi += problem.items[i].upper;
  }
  if (sumLo > total) {
    deficit = maxB(deficit, sumLo - total);
    violations.push(
      `item lower bounds need at least ${sumLo}, budget is ${total} (short ${sumLo - total})`,
    );
  }
  if (sumHi < total) {
    violations.push(
      `item upper bounds allow at most ${sumHi}, budget is ${total} (excess ${total - sumHi})`,
    );
  }

  for (const g of problem.groups) {
    // Members can receive at most total minus the lowers of non-members.
    let outsideLo = 0n;
    const inGroup = new Set(g.memberIdx);
    for (let i = 0; i < problem.items.length; i++) {
      if (!inGroup.has(i)) outsideLo += problem.items[i].lower;
    }
    if (g.lower !== null) {
      const achievable = total - outsideLo;
      if (achievable < g.lower) {
        deficit = maxB(deficit, g.lower - achievable);
        violations.push(
          `group "${g.id}" needs >= ${g.lower} but at most ${achievable} can reach its members`,
        );
      }
    }
    if (g.upper !== null) {
      let memberLo = 0n;
      for (const m of g.memberIdx) memberLo += problem.items[m].lower;
      if (memberLo > g.upper) {
        violations.push(
          `group "${g.id}" allows <= ${g.upper} but member lower bounds already sum to ${memberLo}`,
        );
      }
    }
  }

  for (let i = 0; i < problem.items.length; i++) {
    if (bounds.lo[i] > bounds.hi[i]) {
      violations.push(
        `item "${problem.items[i].id}": propagated bounds are empty ` +
          `[${bounds.lo[i]} > ${bounds.hi[i]}]`,
      );
    }
  }

  return { feasible: violations.length === 0, violations, deficit };
}
