/**
 * Branch and bound over integer tier choices.
 *
 * Lower bound at a node: committed cost + sum of per-component minimum tier
 * costs over unassigned components (valid: any completion pays at least the
 * cheapest tier of each remaining component; dependencies/exclusions can
 * only raise it).
 *
 * Error pruning uses an OPTIMISTIC achievable error for the subtree:
 * - independent source: committed |a|*e sum + sum of per-component minimum
 *   |a|*e over unassigned components.
 * - shared source: committed signed sum C; unassigned components can shift
 *   it by at most R = sum of max_tier |a*e|, so the best achievable absolute
 *   value is max(0, |C| - R).
 * If the optimistic total already exceeds the budget, the subtree is
 * infeasible and pruned. This bound is exact-arithmetic and sound.
 *
 * Budget exhaustion NEVER claims optimality: status becomes 'undecided'
 * and the proven lower bound is the minimum node lower bound over the
 * unexplored frontier.
 */
import { ZERO, add, mul, abs, cmp, min as rmin } from './rational.ts';
import type { Rational } from './rational.ts';
import type { Instance } from './model.ts';
import { evaluateError, checkConstraints } from './error.ts';
import type { Selection, ErrorReport } from './error.ts';

export interface SolveOptions {
  /** maximum number of tree nodes to visit; undefined = unlimited */
  nodeBudget?: number;
}

export type SolveStatus = 'optimal' | 'infeasible' | 'undecided';

export interface Incumbent {
  readonly selection: Selection;
  readonly cost: Rational;
  readonly error: ErrorReport;
}

export interface SolveResult {
  readonly status: SolveStatus;
  /** best feasible solution found (upper bound on optimal cost), if any */
  readonly incumbent: Incumbent | null;
  /**
   * Proven lower bound on the optimal feasible cost.
   * null means +infinity (instance proven infeasible).
   */
  readonly lowerBound: Rational | null;
  readonly nodesExplored: number;
  readonly nodesPrunedCost: number;
  readonly nodesPrunedError: number;
  readonly leavesEvaluated: number;
  readonly budgetExhausted: boolean;
}

interface SourceState {
  readonly kind: 'shared' | 'independent';
  /** committed sum: signed (shared) or sum of |a|*e (independent) */
  committed: Rational;
}

export function solve(inst: Instance, options: SolveOptions = {}): SolveResult {
  const n = inst.components.length;
  // Component visit order: fewest tiers first (stable).
  const order = inst.components
    .map((c, i) => ({ i, tiers: c.tiers.length }))
    .sort((a, b) => a.tiers - b.tiers || a.i - b.i)
    .map((e) => e.i);

  const minTierCost = inst.components.map((c) =>
    c.tiers.map((t) => t.cost).reduce((a, b) => rmin(a, b)),
  );
  // suffixMinCost[k] = sum of min tier costs over order positions k..n-1
  const suffixMinCost: Rational[] = new Array(n + 1).fill(ZERO);
  for (let k = n - 1; k >= 0; k--) {
    suffixMinCost[k] = add(minTierCost[order[k]], suffixMinCost[k + 1]);
  }

  const sourceIndex = new Map(inst.sources.map((s, i) => [s.id, i]));
  const sourceKind = inst.sources.map((s) => s.kind);

  // Per component, per source: contributions for each tier.
  // independent: |a|*e_t ; shared: signed a*e_t (and |a*e_t| for the range bound)
  const perTier = inst.components.map((c) =>
    inst.sources.map((_, si) => {
      const src = inst.sources[si];
      const a = c.sensitivities.get(src.id);
      if (a === undefined) return null;
      return c.tiers.map((t) => {
        const signed = mul(a, t.error);
        return { signed, magnitude: abs(signed) };
      });
    }),
  );

  // Optimistic remaining-error helpers.
  // independent: min over tiers of |a|*e ; shared: max over tiers of |a*e|
  const minInd = inst.components.map((c, ci) =>
    inst.sources.map((_, si) => {
      const arr = perTier[ci][si];
      if (!arr || sourceKind[si] !== 'independent') return ZERO;
      return arr.map((x) => x.magnitude).reduce((p, q) => rmin(p, q));
    }),
  );
  const maxShared = inst.components.map((c, ci) =>
    inst.sources.map((_, si) => {
      const arr = perTier[ci][si];
      if (!arr || sourceKind[si] !== 'shared') return ZERO;
      return arr.map((x) => x.magnitude).reduce((p, q) => (cmp(p, q) >= 0 ? p : q));
    }),
  );

  const assignment: (number | null)[] = new Array(n).fill(null);
  const committed: Rational[] = inst.sources.map(() => ZERO);

  let nodesExplored = 0;
  let nodesPrunedCost = 0;
  let nodesPrunedError = 0;
  let leavesEvaluated = 0;
  let budgetLeft = options.nodeBudget === undefined ? Infinity : options.nodeBudget;
  let budgetExhausted = false;
  let frontierLB: Rational | null = null; // min node-LB over unexplored frontier

  let incumbent: Incumbent | null = null;

  const noteFrontier = (lb: Rational) => {
    if (frontierLB === null || cmp(lb, frontierLB) < 0) frontierLB = lb;
  };

  /** optimistic achievable total error from this node downward */
  const optimisticError = (depth: number): Rational => {
    let total = ZERO;
    for (let si = 0; si < inst.sources.length; si++) {
      if (sourceKind[si] === 'independent') {
        let s = committed[si];
        for (let k = depth; k < n; k++) s = add(s, minInd[order[k]][si]);
        total = add(total, s);
      } else {
        // best achievable |C + x| with x in [-R, R] is max(0, |C| - R)
        let range = ZERO;
        for (let k = depth; k < n; k++) range = add(range, maxShared[order[k]][si]);
        const cAbs = abs(committed[si]);
        const diff = cmp(cAbs, range) > 0 ? add(cAbs, { num: -range.num, den: range.den }) : ZERO;
        total = add(total, diff);
      }
    }
    return total;
  };

  /** can component ci take tier ti given current partial assignment? */
  const consistentWithAssigned = (ci: number, ti: number): boolean => {
    const compId = inst.components[ci].id;
    const tierId = inst.components[ci].tiers[ti].id;
    for (const ex of inst.exclusions) {
      for (const [self, other] of [
        [ex.a, ex.b],
        [ex.b, ex.a],
      ] as const) {
        if (self.component === compId && self.tier === tierId) {
          const oi = inst.components.findIndex((c) => c.id === other.component);
          const oa = assignment[oi];
          if (oa !== null && inst.components[oi].tiers[oa].id === other.tier) return false;
        }
      }
    }
    for (const dep of inst.dependencies) {
      // assigning the antecedent: consequent (if assigned) must match
      if (dep.ifTier.component === compId && dep.ifTier.tier === tierId) {
        const ri = inst.components.findIndex((c) => c.id === dep.requires.component);
        const ra = assignment[ri];
        if (ra !== null && inst.components[ri].tiers[ra].id !== dep.requires.tier) return false;
      }
      // assigning the consequent wrongly while antecedent is assigned
      if (dep.requires.component === compId && dep.requires.tier !== tierId) {
        const ai = inst.components.findIndex((c) => c.id === dep.ifTier.component);
        const aa = assignment[ai];
        if (aa !== null && inst.components[ai].tiers[aa].id === dep.ifTier.tier) return false;
      }
    }
    return true;
  };

  const dfs = (depth: number, costSoFar: Rational): void => {
    const nodeLB = add(costSoFar, suffixMinCost[depth]);
    if (budgetLeft <= 0) {
      budgetExhausted = true;
      noteFrontier(nodeLB);
      return;
    }
    nodesExplored++;
    budgetLeft--;

    if (incumbent !== null && cmp(nodeLB, incumbent.cost) >= 0) {
      nodesPrunedCost++;
      return;
    }
    if (cmp(optimisticError(depth), inst.errorBudget) > 0) {
      nodesPrunedError++;
      return;
    }

    if (depth === n) {
      leavesEvaluated++;
      const selection = new Map<string, string>(
        inst.components.map((c, ci) => [c.id, c.tiers[assignment[ci]!].id]),
      );
      if (checkConstraints(inst, selection).length > 0) return;
      const error = evaluateError(inst, selection);
      if (!error.feasible) return;
      if (incumbent === null || cmp(costSoFar, incumbent.cost) < 0) {
        incumbent = { selection, cost: costSoFar, error };
      }
      return;
    }

    const ci = order[depth];
    const comp = inst.components[ci];
    for (let ti = 0; ti < comp.tiers.length; ti++) {
      if (!consistentWithAssigned(ci, ti)) continue;
      assignment[ci] = ti;
      // commit source contributions
      const deltas: [number, Rational][] = [];
      for (let si = 0; si < inst.sources.length; si++) {
        const arr = perTier[ci][si];
        if (!arr) continue;
        const d = sourceKind[si] === 'shared' ? arr[ti].signed : arr[ti].magnitude;
        committed[si] = add(committed[si], d);
        deltas.push([si, d]);
      }
      dfs(depth + 1, add(costSoFar, comp.tiers[ti].cost));
      for (const [si, d] of deltas) committed[si] = add(committed[si], { num: -d.num, den: d.den });
      assignment[ci] = null;
    }
  };

  dfs(0, ZERO);

  if (budgetExhausted) {
    const lb =
      frontierLB !== null && (incumbent === null || cmp(frontierLB, incumbent.cost) < 0)
        ? frontierLB
        : incumbent !== null
          ? incumbent.cost
          : frontierLB;
    return {
      status: 'undecided',
      incumbent,
      lowerBound: lb,
      nodesExplored,
      nodesPrunedCost,
      nodesPrunedError,
      leavesEvaluated,
      budgetExhausted: true,
    };
  }

  if (incumbent === null) {
    return {
      status: 'infeasible',
      incumbent: null,
      lowerBound: null, // +infinity: proven infeasible
      nodesExplored,
      nodesPrunedCost,
      nodesPrunedError,
      leavesEvaluated,
      budgetExhausted: false,
    };
  }
  return {
    status: 'optimal',
    incumbent,
    lowerBound: incumbent.cost, // search completed: bound is proven
    nodesExplored,
    nodesPrunedCost,
    nodesPrunedError,
    leavesEvaluated,
    budgetExhausted: false,
  };
}
