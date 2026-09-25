/**
 * Exact solver for complete and partial assignments.
 *
 * Scale: small finite problems (exhaustive exact search). Positions are
 * assigned one canonical subject each; a subject may hold several positions
 * of an SoD group only when an explicit exception covers it.
 *
 * Search strategy: backtracking with MRV variable ordering, candidate
 * ordering by cost, and lexicographic branch-and-bound over
 * (changes, assignmentCost, exceptionCost, exceptionCount).
 *
 * Three outcomes:
 *  - a complete assignment inside the exception budget ("satisfied");
 *  - a complete assignment exists but always needs more budget ("undecided",
 *    budget exhausted — nothing is reported as resolved);
 *  - no complete assignment exists even with unlimited exceptions for the
 *    groups that have any exception ("infeasible", minimum conflict core).
 */
import type {
  Assignment,
  ExceptionUse,
  Problem,
  SodGroup,
  SolveResult,
} from "./model.ts";
import {
  IdentityResolver,
  positionDomain,
} from "./policy.ts";
import { validateProblem } from "./validate.ts";

interface SearchPlan {
  complete: boolean;
  assignment: Assignment;
  exceptionsUsed: ExceptionUse[];
  exceptionCost: number;
  assignmentCost: number;
  changes: number;
  exceptionCount: number;
}

interface KeyedGroup {
  group: SodGroup;
  /** canonical subject ids that may use an exception for this group. */
  allowed: Set<string>;
  cheapest: Map<string, number>;
  exceptionIds: Map<string, string>;
}

function lexLess(a: SearchPlan, b: SearchPlan): boolean {
  const key = (p: SearchPlan): number[] => [
    p.changes,
    p.assignmentCost,
    p.exceptionCost,
    p.exceptionCount,
  ];
  const ka = key(a);
  const kb = key(b);
  for (let i = 0; i < ka.length; i++) {
    if ((ka[i] ?? 0) !== (kb[i] ?? 0)) return (ka[i] ?? 0) < (kb[i] ?? 0);
  }
  return false;
}

function describePlan(p: SearchPlan | null): Pick<
  SolveResult,
  "assignment" | "exceptionsUsed" | "exceptionCost" | "assignmentCost" | "changesFromPrevious"
> {
  if (!p) {
    return {
      assignment: null,
      exceptionsUsed: [],
      exceptionCost: 0,
      assignmentCost: 0,
      changesFromPrevious: 0,
    };
  }
  return {
    assignment: p.assignment,
    exceptionsUsed: p.exceptionsUsed,
    exceptionCost: p.exceptionCost,
    assignmentCost: p.assignmentCost,
    changesFromPrevious: p.changes,
  };
}

export interface SolveOptions {
  /**
   * When true, non-null entries of the prior assignment are hard constraints
   * (used for partial-assignment extendability). When false (default repair
   * mode) the prior assignment is only the baseline used to count changes.
   */
  fixed?: boolean;
}

/**
 * Core search entry. `previous` is a (possibly partial) prior assignment;
 * changed positions are counted after alias canonicalisation.
 */
export function solveAssignment(
  problem: Problem,
  previous?: Assignment,
  resolverOrOptions?: IdentityResolver | SolveOptions,
  maybeOptions?: SolveOptions,
): SolveResult {
  const options: SolveOptions =
    resolverOrOptions instanceof IdentityResolver ? maybeOptions ?? {} : resolverOrOptions ?? {};
  const resolver =
    resolverOrOptions instanceof IdentityResolver
      ? resolverOrOptions
      : new IdentityResolver(problem);

  for (const err of validateProblem(problem)) {
    return {
      status: "infeasible",
      ...describePlan(null),
      conflictSet: null,
      reasons: [`invalid problem: ${err}`],
    };
  }

  const budget = problem.exceptionBudget ?? 0;
  const domains = new Map<string, string[]>();
  for (const position of problem.positions) {
    domains.set(position.id, positionDomain(problem, resolver, position.id));
  }

  // Baseline for change counting (repair mode). Canonicalised; an
  // unrecognised/ineligible prior holder counts as "no prior holder".
  const baseline = new Map<string, string>();
  if (previous) {
    for (const position of problem.positions) {
      const raw = previous[position.id];
      if (raw === undefined || raw === null) continue;
      const canonical = resolver.resolve(raw);
      if (canonical !== null && domains.get(position.id)!.includes(canonical)) {
        baseline.set(position.id, canonical);
      }
    }
  }

  // Hard-fixed entries (extendability mode only).
  const fixed = new Map<string, string>();
  if (options.fixed && previous) {
    for (const position of problem.positions) {
      const raw = previous[position.id];
      if (raw === undefined || raw === null) continue;
      const canonical = resolver.resolve(raw);
      if (canonical === null || !domains.get(position.id)!.includes(canonical)) {
        fixed.set(position.id, `__INVALID__${raw}`);
      } else {
        fixed.set(position.id, canonical);
      }
    }
  }

  const unfilled = problem.positions.map((p) => p.id).filter((pid) => !fixed.has(pid));
  const groups = buildGroups(problem, resolver);

  const fixedHardConflict = hasFixedHardConflict(fixed, groups);
  const fixedInvalid = [...fixed.values()].some((v) => v.startsWith("__INVALID__"));

  if (!fixedHardConflict && !fixedInvalid) {
    const capped = search(problem, domains, fixed, unfilled, groups, budget, true, baseline);
    if (capped.complete) return finishSatisfied(capped);
    const uncapped = search(problem, domains, fixed, unfilled, groups, budget, false, baseline);
    if (uncapped.complete) {
      return {
        status: "undecided",
        ...describePlan(null),
        conflictSet: null,
        reasons: [
          `a complete assignment exists but needs exception cost ${uncapped.exceptionCost} over budget ${budget}; budget exhausted, left undecided`,
        ],
      };
    }
  }

  const core = minimumConflictCore(problem, domains, fixed, groups);
  return {
    status: "infeasible",
    ...describePlan(null),
    conflictSet: core,
    reasons: [
      core
        ? `no complete assignment exists; minimum conflicting position set (${core.length}): ${core.join(", ")}`
        : "no complete assignment exists",
    ],
  };
}

function finishSatisfied(plan: SearchPlan): SolveResult {
  return {
    status: "satisfied",
    ...describePlan(plan),
    conflictSet: null,
    reasons: [
      `complete feasible assignment; changed positions: ${plan.changes}, assignment cost: ${plan.assignmentCost}, exception cost: ${plan.exceptionCost}`,
    ],
  };
}

function buildGroups(problem: Problem, resolver: IdentityResolver): KeyedGroup[] {
  const out: KeyedGroup[] = [];
  for (const group of problem.sodGroups ?? []) {
    const allowed = new Set<string>();
    const cheapest = new Map<string, number>();
    const exceptionIds = new Map<string, string>();
    for (const ex of problem.exceptions ?? []) {
      if (ex.group !== group.id) continue;
      const canonical = resolver.resolve(ex.subject);
      if (canonical === null) continue;
      allowed.add(canonical);
      const cost = ex.cost ?? 0;
      const known = cheapest.get(canonical);
      if (known === undefined || cost < known) {
        cheapest.set(canonical, cost);
        exceptionIds.set(canonical, ex.id);
      }
    }
    out.push({ group, allowed, cheapest, exceptionIds });
  }
  return out;
}

function hasFixedHardConflict(fixed: Map<string, string>, groups: KeyedGroup[]): boolean {
  for (const { group, allowed } of groups) {
    const seen = new Map<string, string>();
    for (const pid of group.positions) {
      const subject = fixed.get(pid);
      if (subject === undefined || subject.startsWith("__INVALID__")) continue;
      const prior = seen.get(subject);
      if (prior !== undefined && !allowed.has(subject)) return true;
      seen.set(subject, pid);
    }
  }
  return false;
}

function search(
  problem: Problem,
  domains: Map<string, string[]>,
  fixed: Map<string, string>,
  unfilled: string[],
  groups: KeyedGroup[],
  budget: number,
  cap: boolean,
  baseline: Map<string, string>,
): SearchPlan {
  const current = new Map<string, string>(fixed);
  const positions = problem.positions.map((p) => p.id);
  const costTable = problem.costs ?? {};
  let best: SearchPlan | null = null;

  const prevOf = (pid: string): string | null => baseline.get(pid) ?? null;

  const exceptionsOf = (assign: Map<string, string>): ExceptionUse[] => {
    const uses: ExceptionUse[] = [];
    for (const { group, allowed, cheapest, exceptionIds } of groups) {
      const holders = new Map<string, string[]>();
      for (const pid of group.positions) {
        const subject = assign.get(pid);
        if (subject === undefined) continue;
        const list = holders.get(subject) ?? [];
        list.push(pid);
        holders.set(subject, list);
      }
      for (const [subject, pids] of holders) {
        if (pids.length <= 1 || !allowed.has(subject)) continue;
        uses.push({
          exceptionId: exceptionIds.get(subject)!,
          group: group.id,
          subject,
          cost: cheapest.get(subject)!,
          positions: [...pids],
        });
      }
    }
    uses.sort((a, b) => a.exceptionId.localeCompare(b.exceptionId));
    return uses;
  };

  const dfs = (remaining: string[], changes: number, assignmentCost: number): void => {
    if (remaining.length === 0) {
      const uses = exceptionsOf(current);
      const exceptionCost = uses.reduce((n, u) => n + u.cost, 0);
      if (cap && exceptionCost > budget) return;
      const candidate: SearchPlan = {
        complete: true,
        assignment: {},
        exceptionsUsed: uses,
        exceptionCost,
        assignmentCost,
        changes,
        exceptionCount: uses.length,
      };
      for (const pid of positions) candidate.assignment[pid] = current.get(pid) ?? null;
      if (!best || lexLess(candidate, best)) best = candidate;
      return;
    }

    // MRV: smallest legal domain first.
    let idx = -1;
    let bestSize = Infinity;
    for (let i = 0; i < remaining.length; i++) {
      const size = legalValues(remaining[i]!, domains.get(remaining[i]!)!, current, groups).length;
      if (size < bestSize) {
        bestSize = size;
        idx = i;
      }
    }
    if (bestSize === 0) return;
    const pid = remaining[idx]!;
    const rest = [...remaining.slice(0, idx), ...remaining.slice(idx + 1)];

    const values = orderedValues(pid, domains.get(pid)!, current, groups, costTable, prevOf);
    for (const subject of values) {
      current.set(pid, subject);
      const before = prevOf(pid);
      const extra = before === null ? 0 : before === subject ? 0 : 1;
      const addCost = costTable[pid]?.[subject] ?? 0;
      if (best && cannotImprove(changes + extra, assignmentCost + addCost, best)) {
        current.delete(pid);
        continue;
      }
      dfs(rest, changes + extra, assignmentCost + addCost);
      current.delete(pid);
    }
  };

  dfs(unfilled, 0, 0);
  return best ?? {
    complete: false,
    assignment: {},
    exceptionsUsed: [],
    exceptionCost: 0,
    assignmentCost: 0,
    changes: 0,
    exceptionCount: 0,
  };
}

/**
 * Values that do not create a *hard* conflict. Occupying another position of
 * a group is allowed when an exception covers that subject; the budget is
 * enforced only at leaf time.
 */
function legalValues(
  pid: string,
  domain: string[],
  current: Map<string, string>,
  groups: KeyedGroup[],
): string[] {
  const applicable = groups.filter(({ group }) => group.positions.includes(pid));
  return domain.filter((subject) =>
    applicable.every(({ group, allowed }) => {
      const count = group.positions.filter(
        (other) => other !== pid && current.get(other) === subject,
      ).length;
      return count === 0 || allowed.has(subject);
    }),
  );
}

function orderedValues(
  pid: string,
  domain: string[],
  current: Map<string, string>,
  groups: KeyedGroup[],
  costTable: Record<string, Record<string, number>>,
  prevOf: (pid: string) => string | null,
): string[] {
  const legal = legalValues(pid, domain, current, groups);
  const prev = prevOf(pid);
  return legal.sort((a, b) => {
    if (prev) {
      if (a === prev) return -1;
      if (b === prev) return 1;
    }
    return (costTable[pid]?.[a] ?? 0) - (costTable[pid]?.[b] ?? 0) || a.localeCompare(b);
  });
}

function cannotImprove(changesSoFar: number, costSoFar: number, best: SearchPlan): boolean {
  if (changesSoFar > best.changes) return true;
  if (changesSoFar === best.changes && costSoFar > best.assignmentCost) return true;
  return false;
}

/**
 * Minimum-cardinality set of positions with no jointly feasible assignment.
 * Enumeration by size; feasibility ignores exception budgets (only groups
 * without any applicable exception are hard). Fixed assignments are honoured.
 */
function minimumConflictCore(
  problem: Problem,
  domains: Map<string, string[]>,
  fixed: Map<string, string>,
  groups: KeyedGroup[],
): string[] | null {
  const all = problem.positions.map((p) => p.id);

  const feasible = (subset: string[]): boolean => {
    const inSubset = new Set(subset);
    const current = new Map<string, string>();
    for (const pid of subset) {
      const v = fixed.get(pid);
      if (v !== undefined) {
        if (v.startsWith("__INVALID__")) return false;
        current.set(pid, v);
      }
    }
    const open = subset.filter((pid) => !fixed.has(pid));

    const go = (remaining: string[]): boolean => {
      if (remaining.length === 0) return true;
      let idx = -1;
      let bestSize = Infinity;
      for (let i = 0; i < remaining.length; i++) {
        const pid = remaining[i]!;
        const size = (domains.get(pid) ?? []).filter((subject) =>
          groups
            .filter(({ group }) => group.positions.includes(pid) && group.positions.some((q) => inSubset.has(q)))
            .every(({ group, allowed }) => {
              const count = group.positions.filter(
                (other) => inSubset.has(other) && other !== pid && current.get(other) === subject,
              ).length;
              return count === 0 || allowed.has(subject);
            }),
        ).length;
        if (size < bestSize) {
          bestSize = size;
          idx = i;
        }
      }
      if (bestSize === 0) return false;
      const pid = remaining[idx]!;
      const rest = [...remaining.slice(0, idx), ...remaining.slice(idx + 1)];
      for (const subject of domains.get(pid) ?? []) {
        const ok = groups
          .filter(({ group }) => group.positions.includes(pid) && group.positions.some((q) => inSubset.has(q)))
          .every(({ group, allowed }) => {
            const count = group.positions.filter(
              (other) => inSubset.has(other) && other !== pid && current.get(other) === subject,
            ).length;
            return count === 0 || allowed.has(subject);
          });
        if (!ok) continue;
        current.set(pid, subject);
        if (go(rest)) {
          current.delete(pid);
          return true;
        }
        current.delete(pid);
      }
      return false;
    };

    return go(open);
  };

  for (let size = 1; size <= all.length; size++) {
    for (const combo of combinations(all, size)) {
      if (!feasible(combo)) return combo;
    }
  }
  return null;
}

function* combinations<T>(items: T[], size: number): Generator<T[]> {
  const indices = Array.from({ length: size }, (_, i) => i);
  const n = items.length;
  if (size > n) return;
  yield indices.map((i) => items[i]!);
  while (true) {
    let i = size - 1;
    while (i >= 0 && indices[i] === i + n - size) i--;
    if (i < 0) return;
    indices[i] = indices[i]! + 1;
    for (let j = i + 1; j < size; j++) indices[j] = indices[j - 1]! + 1;
    yield indices.map((k) => items[k]!);
  }
}
