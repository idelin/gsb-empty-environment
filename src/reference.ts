/**
 * Finite-enumeration reference checker.
 *
 * The primary limit solver uses difference-constraint propagation; this
 * reference independently enumerates every assignment from a finite
 * candidate domain and computes the exact feasible projection per variable.
 * The domain is the closure of the constraint constants under addition
 * (bounded by the number of variables), which covers every bound derivable
 * by propagation along simple paths. Tests cross-check the solver against
 * this brute-force ground truth.
 */

import type { LimitObligation } from './obligations.ts';
import { constraintsOf, ZERO, type LimitConstraint } from './limits.ts';

const MAX_ASSIGNMENTS = 4_000_000;
const MAX_DOMAIN = 512;

function candidateDomain(constraints: readonly LimitConstraint[], varCount: number): number[] {
  const base = new Set<number>();
  for (const c of constraints) {
    base.add(c.c);
    base.add(-c.c);
  }
  base.add(0);
  // Close under addition: any propagated bound is a sum of at most
  // (varCount + 1) constants along a simple path.
  let domain = new Set<number>(base);
  for (let i = 0; i <= varCount; i++) {
    const next = new Set<number>(domain);
    for (const x of domain) {
      for (const y of base) {
        next.add(x + y);
        if (next.size > MAX_DOMAIN) {
          throw new Error(`finite enumeration domain too large (> ${MAX_DOMAIN})`);
        }
      }
    }
    domain = next;
  }
  return [...domain];
}

export type EnumerationResult =
  | {
      ok: true;
      feasibleAssignments: number;
      /** exact feasible projection per variable (within the finite domain) */
      projection: ReadonlyMap<string, { min: number; max: number }>;
      domainMin: number;
      domainMax: number;
    }
  | { ok: false };

export function enumerateLimitSolutions(obs: readonly LimitObligation[]): EnumerationResult {
  const constraints = obs.flatMap(constraintsOf);
  const vars = [...new Set(constraints.flatMap((c) => [c.a, c.b]))].filter((v) => v !== ZERO);
  const domain = candidateDomain(constraints, vars.length);
  let combos = 1;
  for (let i = 0; i < vars.length; i++) combos *= domain.length;
  if (combos > MAX_ASSIGNMENTS) {
    throw new Error(`finite enumeration domain too large: ${combos} assignments`);
  }
  const assign = new Map<string, number>();
  const projection = new Map<string, { min: number; max: number }>();
  let feasibleAssignments = 0;

  const satisfies = (): boolean => {
    for (const c of constraints) {
      const va = c.a === ZERO ? 0 : assign.get(c.a)!;
      const vb = c.b === ZERO ? 0 : assign.get(c.b)!;
      if (va - vb > c.c) return false;
    }
    return true;
  };

  const visit = (idx: number): void => {
    if (idx === vars.length) {
      if (!satisfies()) return;
      feasibleAssignments++;
      for (const v of vars) {
        const val = assign.get(v)!;
        const p = projection.get(v);
        if (!p) projection.set(v, { min: val, max: val });
        else {
          if (val < p.min) p.min = val;
          if (val > p.max) p.max = val;
        }
      }
      return;
    }
    for (const d of domain) {
      assign.set(vars[idx], d);
      visit(idx + 1);
    }
  };
  visit(0);

  if (feasibleAssignments === 0) return { ok: false };
  return {
    ok: true,
    feasibleAssignments,
    projection,
    domainMin: Math.min(...domain),
    domainMax: Math.max(...domain),
  };
}

/**
 * Independent boundedness check: a variable has a finite upper bound iff it
 * is reachable from ZERO in the constraint graph (plain BFS - deliberately
 * not the solver's Bellman-Ford, so the two cross-check each other).
 */
function reachableFromZero(constraints: readonly LimitConstraint[], reversed: boolean): Set<string> {
  const adj = new Map<string, string[]>();
  for (const c of constraints) {
    const [from, to] = reversed ? [c.a, c.b] : [c.b, c.a];
    (adj.get(from) ?? adj.set(from, []).get(from)!).push(to);
  }
  const seen = new Set<string>([ZERO]);
  const queue = [ZERO];
  while (queue.length > 0) {
    const n = queue.shift()!;
    for (const m of adj.get(n) ?? []) {
      if (!seen.has(m)) {
        seen.add(m);
        queue.push(m);
      }
    }
  }
  seen.delete(ZERO);
  return seen;
}

/**
 * Cross-check the solver against the reference: feasibility must agree,
 * boundedness per direction must agree (BFS reachability), and every
 * tightest bound must equal the exact feasible projection (enumeration).
 */
export function referenceCheckLimits(
  obs: readonly LimitObligation[],
  solved:
    | { ok: true; upper: ReadonlyMap<string, { value: number }>; lower: ReadonlyMap<string, { value: number }> }
    | { ok: false },
): boolean {
  const ref = enumerateLimitSolutions(obs);
  if (ref.ok !== solved.ok) return false;
  if (!ref.ok || !solved.ok) return true;
  const constraints = obs.flatMap(constraintsOf);
  const boundedAbove = reachableFromZero(constraints, false);
  const boundedBelow = reachableFromZero(constraints, true);
  for (const [v, { min, max }] of ref.projection) {
    const up = solved.upper.get(v);
    const lo = solved.lower.get(v);
    if (boundedAbove.has(v) !== (up !== undefined)) return false;
    if (boundedBelow.has(v) !== (lo !== undefined)) return false;
    if (up && up.value !== max) return false;
    if (lo && lo.value !== min) return false;
  }
  for (const v of solved.upper.keys()) if (!ref.projection.has(v)) return false;
  for (const v of solved.lower.keys()) if (!ref.projection.has(v)) return false;
  return true;
}

/** Verify a conflict core by enumeration: infeasible, and every proper subset feasible. */
export function referenceCheckCore(core: readonly LimitObligation[]): boolean {
  if (core.length === 0) return false;
  if (enumerateLimitSolutions(core).ok) return false;
  return core.every((ob) => enumerateLimitSolutions(core.filter((x) => x !== ob)).ok);
}
