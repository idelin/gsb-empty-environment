import { evaluatePolicy } from './policy.ts';
import type {
  Assignment,
  HallDeficiency,
  Position,
  Problem,
  SolveResult,
  Subject,
} from './types.ts';

export class DirectoryError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DirectoryError';
  }
}

export interface Directory {
  readonly subjects: readonly Subject[];
  /** Every known name (id or alias) -> canonical subject. */
  readonly byName: ReadonlyMap<string, Subject>;
}

export function buildDirectory(subjects: readonly Subject[]): Directory {
  const seenIds = new Set<string>();
  const byName = new Map<string, Subject>();
  for (const subject of subjects) {
    if (seenIds.has(subject.id)) {
      throw new DirectoryError(`duplicate subject id "${subject.id}"`);
    }
    seenIds.add(subject.id);
    for (const name of [subject.id, ...(subject.aliases ?? [])]) {
      const existing = byName.get(name);
      if (existing !== undefined && existing.id !== subject.id) {
        throw new DirectoryError(
          `name "${name}" is claimed by both "${existing.id}" and "${subject.id}"`,
        );
      }
      byName.set(name, subject);
    }
  }
  return { subjects, byName };
}

interface NormalizedException {
  readonly id: string;
  readonly positions: ReadonlySet<string>;
  readonly subjectId: string;
}

function normalizeExceptions(
  problem: Problem,
  dir: Directory,
): NormalizedException[] {
  return (problem.exceptions ?? []).map((exception) => {
    const subject = dir.byName.get(exception.subject);
    if (subject === undefined) {
      throw new DirectoryError(
        `exception "${exception.id}" references unknown subject "${exception.subject}"`,
      );
    }
    return {
      id: exception.id,
      positions: new Set(exception.positions),
      subjectId: subject.id,
    };
  });
}

function* combinations<T>(items: readonly T[], k: number): Generator<T[]> {
  if (k === 0) {
    yield [];
    return;
  }
  for (let i = 0; i <= items.length - k; i++) {
    for (const rest of combinations(items.slice(i + 1), k - 1)) {
      yield [items[i] as T, ...rest];
    }
  }
}

interface ConflictPair {
  readonly a: string;
  readonly b: string;
  readonly subjectId: string;
}

function conflictPairsOf(
  groups: ReadonlyMap<string, readonly string[]>,
): ConflictPair[] {
  const pairs: ConflictPair[] = [];
  for (const [subjectId, positions] of groups) {
    for (let i = 0; i < positions.length; i++) {
      for (let j = i + 1; j < positions.length; j++) {
        pairs.push({ a: positions[i] as string, b: positions[j] as string, subjectId });
      }
    }
  }
  return pairs;
}

/**
 * Minimum set of exceptions covering every conflicting pair of positions
 * (exact set cover; inputs are small by design). Returns null when some pair
 * has no covering exception at all.
 */
function exceptionsNeeded(
  exceptions: readonly NormalizedException[],
  groups: ReadonlyMap<string, readonly string[]>,
): readonly string[] | null {
  const pairs = conflictPairsOf(groups);
  if (pairs.length === 0) return [];
  const candidateIds: string[] = [];
  const pairCovers: string[][] = [];
  for (const pair of pairs) {
    const covering = exceptions
      .filter(
        (e) =>
          e.subjectId === pair.subjectId &&
          e.positions.has(pair.a) &&
          e.positions.has(pair.b),
      )
      .map((e) => e.id);
    if (covering.length === 0) return null;
    pairCovers.push(covering);
    for (const id of covering) {
      if (!candidateIds.includes(id)) candidateIds.push(id);
    }
  }
  for (let k = 0; k <= candidateIds.length; k++) {
    for (const combo of combinations(candidateIds, k)) {
      const chosen = new Set(combo);
      if (pairCovers.every((covering) => covering.some((id) => chosen.has(id)))) {
        return [...combo].sort();
      }
    }
  }
  return null;
}

interface AnyFeasible {
  readonly assignment: ReadonlyMap<string, string>;
  readonly exceptionsUsed: readonly string[];
}

/** Feasibility check ignoring the exception budget and any objective. */
function findAnyFeasible(
  positions: readonly Position[],
  candidates: ReadonlyMap<string, readonly Subject[]>,
  fixed: Readonly<Record<string, string>>,
  dir: Directory,
  exceptions: readonly NormalizedException[],
): AnyFeasible | null {
  const order = [...positions].sort(
    (x, y) => (candidates.get(x.id)?.length ?? 0) - (candidates.get(y.id)?.length ?? 0),
  );
  const assign = new Map<string, string>();
  const groups = new Map<string, string[]>();

  function recurse(i: number): AnyFeasible | null {
    if (i === order.length) {
      const used = exceptionsNeeded(exceptions, groups);
      return used === null ? null : { assignment: new Map(assign), exceptionsUsed: used };
    }
    const pos = order[i] as Position;
    let options: readonly Subject[];
    const fixedName = fixed[pos.id];
    if (fixedName !== undefined) {
      const subject = dir.byName.get(fixedName);
      if (subject === undefined) return null;
      if (pos.requires && !evaluatePolicy(pos.requires, subject.attributes ?? {})) return null;
      options = [subject];
    } else {
      options = candidates.get(pos.id) ?? [];
    }
    for (const subject of options) {
      assign.set(pos.id, subject.id);
      const group = groups.get(subject.id) ?? [];
      group.push(pos.id);
      groups.set(subject.id, group);
      if (exceptionsNeeded(exceptions, groups) !== null) {
        const found = recurse(i + 1);
        if (found !== null) return found;
      }
      group.pop();
      if (group.length === 0) groups.delete(subject.id);
      assign.delete(pos.id);
    }
    return null;
  }

  return recurse(0);
}

/** Minimum number of positions to drop so the rest becomes feasible. */
function minimalConflictSet(
  problem: Problem,
  dir: Directory,
  exceptions: readonly NormalizedException[],
  candidates: ReadonlyMap<string, readonly Subject[]>,
): string[] {
  const ids = problem.positions.map((p) => p.id);
  const fixed = problem.fixed ?? {};
  for (let k = 1; k <= ids.length; k++) {
    for (const combo of combinations(ids, k)) {
      const removed = new Set(combo);
      const subPositions = problem.positions.filter((p) => !removed.has(p.id));
      const subFixed: Record<string, string> = {};
      for (const [posId, name] of Object.entries(fixed)) {
        if (!removed.has(posId)) subFixed[posId] = name;
      }
      if (findAnyFeasible(subPositions, candidates, subFixed, dir, exceptions) !== null) {
        return combo;
      }
    }
  }
  return [...ids];
}

/**
 * Hall's-condition witness on the qualification graph (exceptions ignored):
 * a set of positions whose neighbourhood of qualified canonical subjects is
 * strictly smaller than the set itself.
 */
export function hallDeficiency(
  positions: readonly Position[],
  candidates: ReadonlyMap<string, readonly Subject[]>,
): HallDeficiency | undefined {
  const matchPosToSubject = new Map<string, string>();
  const matchSubjectToPos = new Map<string, string>();

  function augment(posId: string, seen: Set<string>): boolean {
    for (const subject of candidates.get(posId) ?? []) {
      if (seen.has(subject.id)) continue;
      seen.add(subject.id);
      const current = matchSubjectToPos.get(subject.id);
      if (current === undefined || augment(current, seen)) {
        matchPosToSubject.set(posId, subject.id);
        matchSubjectToPos.set(subject.id, posId);
        return true;
      }
    }
    return false;
  }

  for (const pos of positions) {
    augment(pos.id, new Set());
  }
  if (matchPosToSubject.size === positions.length) return undefined;

  // Alternating reachability from unmatched positions.
  const reachedPositions = new Set<string>();
  const reachedSubjects = new Set<string>();
  const queue: string[] = [];
  for (const pos of positions) {
    if (!matchPosToSubject.has(pos.id)) {
      reachedPositions.add(pos.id);
      queue.push(pos.id);
    }
  }
  while (queue.length > 0) {
    const posId = queue.pop() as string;
    for (const subject of candidates.get(posId) ?? []) {
      if (reachedSubjects.has(subject.id)) continue;
      reachedSubjects.add(subject.id);
      const next = matchSubjectToPos.get(subject.id);
      if (next !== undefined && !reachedPositions.has(next)) {
        reachedPositions.add(next);
        queue.push(next);
      }
    }
  }
  return {
    positions: [...reachedPositions].sort(),
    subjects: [...reachedSubjects].sort(),
  };
}

export interface SolveOptions {
  /** Safety cap on search nodes; exceeding it yields a `pending` result. */
  readonly searchNodeBudget?: number;
}

/**
 * Exact small-scale solver.
 *
 * Objective (lexicographic): first minimise the number of positions whose
 * canonical assignee differs from `existingAssignment`, then minimise total
 * cost. Results:
 * - `feasible`   — complete assignment within the exception budget;
 * - `infeasible` — impossible even with unlimited exceptions; reports the
 *                  minimum conflict position set and, when the qualification
 *                  graph is the bottleneck, a Hall deficiency witness;
 * - `pending`    — undecided in practice: the exception budget (or the search
 *                  node budget) was exhausted before a plan could be issued.
 */
export function solve(problem: Problem, options: SolveOptions = {}): SolveResult {
  const dir = buildDirectory(problem.subjects);
  const exceptions = normalizeExceptions(problem, dir);
  const budget = problem.exceptionBudget ?? 0;

  const positionIds = new Set<string>();
  for (const pos of problem.positions) {
    if (positionIds.has(pos.id)) {
      throw new DirectoryError(`duplicate position id "${pos.id}"`);
    }
    positionIds.add(pos.id);
  }

  const candidates = new Map<string, Subject[]>();
  for (const pos of problem.positions) {
    candidates.set(
      pos.id,
      dir.subjects.filter(
        (s) => !pos.requires || evaluatePolicy(pos.requires, s.attributes ?? {}),
      ),
    );
  }

  const fixed = problem.fixed ?? {};

  // Previous canonical assignee per position; null means the previous name
  // no longer resolves, so any new holder counts as a personnel change.
  const baseline = new Map<string, string | null>();
  for (const [posId, name] of Object.entries(problem.existingAssignment ?? {})) {
    if (!positionIds.has(posId)) continue;
    const subject = dir.byName.get(name);
    baseline.set(posId, subject === undefined ? null : subject.id);
  }

  const costByCanonical = new Map<string, Map<string, number>>();
  for (const [posId, bySubject] of Object.entries(problem.costs ?? {})) {
    const table = new Map<string, number>();
    for (const [name, cost] of Object.entries(bySubject)) {
      if (cost < 0) {
        throw new DirectoryError(`negative cost for position "${posId}", subject "${name}"`);
      }
      const subject = dir.byName.get(name);
      if (subject !== undefined) table.set(subject.id, cost);
    }
    costByCanonical.set(posId, table);
  }

  const order = [...problem.positions].sort((a, b) => {
    const fixedA = fixed[a.id] === undefined ? 1 : 0;
    const fixedB = fixed[b.id] === undefined ? 1 : 0;
    return (
      fixedA - fixedB ||
      (candidates.get(a.id)?.length ?? 0) - (candidates.get(b.id)?.length ?? 0)
    );
  });

  const nodeBudget = options.searchNodeBudget ?? 1_000_000;
  let nodes = 0;
  let searchExhausted = false;

  interface Best {
    readonly assignment: Assignment;
    readonly changes: number;
    readonly cost: number;
    readonly exceptionsUsed: readonly string[];
  }
  let best: Best | null = null;

  const assign = new Map<string, string>();
  const groups = new Map<string, string[]>();

  function recurse(i: number, changes: number, cost: number): void {
    if (searchExhausted) return;
    nodes += 1;
    if (nodes > nodeBudget) {
      searchExhausted = true;
      return;
    }
    if (
      best !== null &&
      (changes > best.changes || (changes === best.changes && cost >= best.cost))
    ) {
      return;
    }
    if (i === order.length) {
      const used = exceptionsNeeded(exceptions, groups);
      if (used === null || used.length > budget) return;
      best = {
        assignment: Object.fromEntries(assign),
        changes,
        cost,
        exceptionsUsed: used,
      };
      return;
    }
    const pos = order[i] as Position;
    let optionsList: readonly Subject[];
    const fixedName = fixed[pos.id];
    if (fixedName !== undefined) {
      const subject = dir.byName.get(fixedName);
      if (subject === undefined) return;
      if (pos.requires && !evaluatePolicy(pos.requires, subject.attributes ?? {})) return;
      optionsList = [subject];
    } else {
      optionsList = candidates.get(pos.id) ?? [];
    }
    for (const subject of optionsList) {
      assign.set(pos.id, subject.id);
      const group = groups.get(subject.id) ?? [];
      group.push(pos.id);
      groups.set(subject.id, group);
      if (exceptionsNeeded(exceptions, groups) !== null) {
        const moved = baseline.has(pos.id) && baseline.get(pos.id) !== subject.id;
        const nextChanges = changes + (moved ? 1 : 0);
        const nextCost = cost + (costByCanonical.get(pos.id)?.get(subject.id) ?? 0);
        recurse(i + 1, nextChanges, nextCost);
      }
      group.pop();
      if (group.length === 0) groups.delete(subject.id);
      assign.delete(pos.id);
    }
  }

  recurse(0, 0, 0);

  if (searchExhausted) {
    return {
      status: 'pending',
      reason: `search node budget (${nodeBudget}) exhausted`,
      unassigned: problem.positions.map((p) => p.id),
    };
  }
  if (best !== null) {
    return {
      status: 'feasible',
      assignment: best.assignment,
      changes: best.changes,
      cost: best.cost,
      exceptionsUsed: best.exceptionsUsed,
    };
  }

  // No plan within budget: decide between "budget exhausted" (pending) and
  // genuinely impossible (infeasible) by relaxing the exception budget.
  const relaxed = findAnyFeasible(problem.positions, candidates, fixed, dir, exceptions);
  if (relaxed !== null) {
    return {
      status: 'pending',
      reason:
        `exception budget (${budget}) exhausted; a plan would need ` +
        `${relaxed.exceptionsUsed.length} exception(s): ${relaxed.exceptionsUsed.join(', ')}`,
      unassigned: [],
      requiredExceptions: relaxed.exceptionsUsed,
    };
  }

  const conflictPositions = minimalConflictSet(problem, dir, exceptions, candidates);
  const hall = hallDeficiency(problem.positions, candidates);
  return hall === undefined
    ? { status: 'infeasible', conflictPositions }
    : { status: 'infeasible', conflictPositions, hallDeficiency: hall };
}
