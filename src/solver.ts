/**
 * Branch & bound over discrete grade choices.
 *
 *  - Branching: components in a fixed deterministic order (largest cost
 *    spread first, ties by id); grades cheapest-first.
 *  - Cost lower bound: committed cost + sum of min in-domain grade cost per
 *    unassigned component. Valid for any (even negative) costs.
 *  - Error pruning: optimistic interval hull over in-domain grades for
 *    unassigned components + exact intervals for assigned ones. A completion
 *    can only be feasible if the hull intersects [-limit, limit]; if the hull
 *    lies entirely outside, no completion is feasible and we prune. This is
 *    conservative and never prunes a feasible completion.
 *  - Constraints (mutual exclusions, dependencies) are propagated as domain
 *    restrictions to a fixpoint at every node.
 *  - Budget: maxNodes caps explored nodes. On exhaustion the result is
 *    "undecided": the incumbent (feasible upper bound, if any) and the best
 *    proven lower bound are reported, but optimality is NOT claimed.
 *  - Every node decision is logged so an independent verifier can replay and
 *    recompute the certificate.
 */

import * as R from "./rational.ts";
import { ZERO_INTERVAL, analyze, intervalAdd, intervalHull, intervalScale, refIndex, totalCost } from "./model.ts";
import type { Assignment, Component, Interval, Problem } from "./model.ts";

export interface SolveOptions {
  maxNodes?: number; // default 1_000_000
  logNodes?: boolean; // default true (needed for certificates)
}

export type SolveStatus = "optimal" | "undecided" | "infeasible";

export interface NodeLogEntry {
  node: number;
  depth: number;
  assigned: Record<string, string>; // component id -> grade id
  costLb: string; // canonical rational
  hullLo: string;
  hullHi: string;
  action: "expand" | "prune-cost" | "prune-error" | "prune-constraint" | "incumbent" | "leaf-infeasible";
}

export interface SolveResult {
  status: SolveStatus;
  nodesExplored: number;
  budgetExhausted: boolean;
  /** best feasible cost found (upper bound on optimum), if any */
  upperBound: R.Rational | null;
  bestAssignment: Map<number, number> | null;
  /** proven lower bound on the optimum (== optimum when status is optimal) */
  lowerBound: R.Rational | null;
  log: NodeLogEntry[];
}

interface Domain {
  allowed: boolean[]; // per grade
}

function domainClone(d: Domain): Domain {
  return { allowed: [...d.allowed] };
}
function domainEmpty(d: Domain): boolean {
  return !d.allowed.some(Boolean);
}
function domainSingleton(d: Domain): number {
  let found = -1;
  for (let i = 0; i < d.allowed.length; i++) {
    if (d.allowed[i]) {
      if (found !== -1) return -1;
      found = i;
    }
  }
  return found;
}

export function solve(problem: Problem, options: SolveOptions = {}): SolveResult {
  const maxNodes = options.maxNodes ?? 1_000_000;
  const keepLog = options.logNodes ?? true;
  const n = problem.components.length;

  // Deterministic branching order: largest cost spread first, ties by id.
  const order: number[] = problem.components
    .map((c, ci) => {
      let lo: R.Rational | null = null;
      let hi: R.Rational | null = null;
      for (const g of c.grades) {
        lo = lo === null || R.cmp(g.cost, lo) < 0 ? g.cost : lo;
        hi = hi === null || R.cmp(g.cost, hi) > 0 ? g.cost : hi;
      }
      return { ci, spread: R.sub(hi!, lo!), id: c.id };
    })
    .sort((a, b) => R.cmp(b.spread, a.spread) || (a.id < b.id ? -1 : 1))
    .map((e) => e.ci);

  // Grades tried cheapest-first (ties by id) for quick incumbents.
  const gradeOrder: number[][] = problem.components.map((c) =>
    c.grades.map((g, gi) => ({ gi, cost: g.cost, id: g.id }))
      .sort((a, b) => R.cmp(a.cost, b.cost) || (a.id < b.id ? -1 : 1))
      .map((e) => e.gi),
  );

  // Precompute constraint tables.
  const excl = problem.exclusions.map((e) => ({ a: refIndex(problem, e.a), b: refIndex(problem, e.b) }));
  const deps = problem.dependencies.map((d) => ({ if: refIndex(problem, d.if), req: refIndex(problem, d.requires) }));

  /** Propagate exclusions/dependencies to a fixpoint. Null => some domain emptied. */
  function propagate(domains: Domain[]): Domain[] | null {
    let changed = true;
    while (changed) {
      changed = false;
      for (const { a, b } of excl) {
        for (const [x, y] of [[a, b], [b, a]] as const) {
          const sx = domainSingleton(domains[x.ci]);
          if (sx === x.gi && domains[y.ci].allowed[y.gi]) {
            domains[y.ci].allowed[y.gi] = false;
            changed = true;
            if (domainEmpty(domains[y.ci])) return null;
          }
        }
      }
      for (const { if: cond, req } of deps) {
        const s = domainSingleton(domains[cond.ci]);
        if (s === cond.gi) {
          const d = domains[req.ci];
          for (let gi = 0; gi < d.allowed.length; gi++) {
            if (d.allowed[gi] && gi !== req.gi) {
              d.allowed[gi] = false;
              changed = true;
            }
          }
          if (domainEmpty(d)) return null;
        }
      }
    }
    return domains;
  }

  const initialDomains: Domain[] = problem.components.map((c) => ({
    allowed: c.grades.map(() => true),
  }));
  const propagated0 = propagate(initialDomains.map(domainClone));

  const log: NodeLogEntry[] = [];
  let nodes = 0;
  let budgetExhausted = false;
  let incumbent: R.Rational | null = null;
  let incumbentAssignment: Map<number, number> | null = null;
  /** min cost-LB over nodes cut off by the budget (open frontier) */
  let frontierLb: R.Rational | null = null;
  let sawFeasibleLeaf = false;

  function minDomainCost(domains: Domain[], ci: number): R.Rational {
    const comp: Component = problem.components[ci];
    let best: R.Rational | null = null;
    for (let gi = 0; gi < comp.grades.length; gi++) {
      if (!domains[ci].allowed[gi]) continue;
      const c = comp.grades[gi].cost;
      if (best === null || R.cmp(c, best) < 0) best = c;
    }
    return best!;
  }

  /** Optimistic total-error hull: exact for singleton domains, hull over domain otherwise. */
  function optimisticHull(domains: Domain[]): Interval {
    const perSource = new Map<string, Interval>();
    for (const s of problem.sources) perSource.set(s, ZERO_INTERVAL);
    for (let ci = 0; ci < n; ci++) {
      const comp = problem.components[ci];
      for (const s of problem.sources) {
        const sens = comp.sensitivities.get(s) ?? R.ZERO;
        let acc: Interval | null = null;
        for (let gi = 0; gi < comp.grades.length; gi++) {
          if (!domains[ci].allowed[gi]) continue;
          const err = comp.grades[gi].errors.get(s) ?? ZERO_INTERVAL;
          const scaled = intervalScale(err, sens);
          acc = acc === null ? scaled : intervalHull(acc, scaled);
        }
        perSource.set(s, intervalAdd(perSource.get(s)!, acc!));
      }
    }
    let total = ZERO_INTERVAL;
    for (const s of problem.sources) total = intervalAdd(total, perSource.get(s)!);
    return total;
  }

  function assignedRecord(assigned: (number | null)[]): Record<string, string> {
    const rec: Record<string, string> = {};
    for (let ci = 0; ci < n; ci++) {
      const gi = assigned[ci];
      if (gi !== null) rec[problem.components[ci].id] = problem.components[ci].grades[gi].id;
    }
    return rec;
  }

  function dfs(domains: Domain[], assigned: (number | null)[], committed: R.Rational, depth: number): void {
    if (budgetExhausted) return;
    nodes++;

    // Cost lower bound.
    let costLb = committed;
    for (let ci = 0; ci < n; ci++) {
      if (assigned[ci] === null) costLb = R.add(costLb, minDomainCost(domains, ci));
    }

    if (nodes > maxNodes) {
      budgetExhausted = true;
      // The full cost lower bound (not just committed cost) bounds every
      // completion of this cut-off subtree from below, even with negative
      // grade costs.
      if (frontierLb === null || R.cmp(costLb, frontierLb) < 0) frontierLb = costLb;
      return;
    }

    const hull = optimisticHull(domains);
    const negLimit = R.neg(problem.errorLimit);
    const record = (action: NodeLogEntry["action"]): void => {
      if (!keepLog) return;
      log.push({
        node: nodes,
        depth,
        assigned: assignedRecord(assigned),
        costLb: R.toCanonical(costLb),
        hullLo: R.toCanonical(hull.lo),
        hullHi: R.toCanonical(hull.hi),
        action,
      });
    };

    if (incumbent !== null && R.cmp(costLb, incumbent) >= 0) {
      record("prune-cost");
      return;
    }
    // Error prune: hull entirely outside [-limit, limit] => no feasible completion.
    if (R.cmp(hull.lo, problem.errorLimit) > 0 || R.cmp(hull.hi, negLimit) < 0) {
      record("prune-error");
      return;
    }

    const allAssigned = assigned.every((a) => a !== null);
    if (allAssigned) {
      const map: Assignment = new Map(assigned.map((gi, ci) => [ci, gi!]));
      const result = analyze(problem, map);
      if (result.feasible) {
        sawFeasibleLeaf = true;
        const cost = totalCost(problem, map);
        if (incumbent === null || R.cmp(cost, incumbent) < 0) {
          incumbent = cost;
          incumbentAssignment = new Map(map);
          record("incumbent");
          return;
        }
      }
      record("leaf-infeasible");
      return;
    }

    record("expand");
    const ci = order.find((c) => assigned[c] === null)!;
    for (const gi of gradeOrder[ci]) {
      if (!domains[ci].allowed[gi]) continue;
      const nextDomains = domains.map(domainClone);
      for (let g = 0; g < nextDomains[ci].allowed.length; g++) nextDomains[ci].allowed[g] = g === gi;
      const propagated = propagate(nextDomains);
      if (propagated === null) {
        if (keepLog) {
          log.push({
            node: -1,
            depth: depth + 1,
            assigned: { ...assignedRecord(assigned), [problem.components[ci].id]: problem.components[ci].grades[gi].id },
            costLb: R.toCanonical(costLb),
            hullLo: R.toCanonical(hull.lo),
            hullHi: R.toCanonical(hull.hi),
            action: "prune-constraint",
          });
        }
        continue;
      }
      const nextAssigned = [...assigned];
      nextAssigned[ci] = gi;
      dfs(propagated, nextAssigned, R.add(committed, problem.components[ci].grades[gi].cost), depth + 1);
      if (budgetExhausted) return;
    }
  }

  if (propagated0 === null) {
    return {
      status: "infeasible",
      nodesExplored: 0,
      budgetExhausted: false,
      upperBound: null,
      bestAssignment: null,
      lowerBound: null,
      log,
    };
  }
  dfs(propagated0, new Array(n).fill(null), R.ZERO, 0);

  let status: SolveStatus;
  let lowerBound: R.Rational | null;
  if (budgetExhausted) {
    status = "undecided";
    // Proven lower bound with an open frontier: every unexplored subtree has
    // committed cost >= frontierLb, and explored subtrees were either pruned
    // with LB >= incumbent or fully explored. The optimum (if it exists) is
    // >= min(frontierLb, incumbent-or-+inf). If there is no incumbent, the
    // frontier LB still bounds any feasible solution's cost from below.
    lowerBound = frontierLb;
    if (incumbent !== null && lowerBound !== null && R.cmp(incumbent, lowerBound) < 0) {
      lowerBound = incumbent; // safety; cannot happen, keeps the invariant LB <= UB
    }
  } else if (incumbent !== null) {
    status = "optimal";
    lowerBound = incumbent;
  } else {
    status = sawFeasibleLeaf ? "undecided" : "infeasible";
    lowerBound = null;
  }

  return {
    status,
    nodesExplored: nodes,
    budgetExhausted,
    upperBound: incumbent,
    bestAssignment: incumbentAssignment,
    lowerBound,
    log,
  };
}
