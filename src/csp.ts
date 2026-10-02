export type Value = number | string;

export interface BinaryConstraint {
  readonly a: number;
  readonly b: number;
  readonly check: (va: Value, vb: Value) => boolean;
  readonly label?: string;
}

export interface CspProblem {
  readonly numVars: number;
  readonly domains: ReadonlyArray<readonly Value[]>;
  readonly constraints: readonly BinaryConstraint[];
  readonly varNames?: readonly string[];
}

export interface Backjump {
  readonly fromLevel: number;
  readonly toLevel: number;
  readonly conflictSet: readonly number[];
}

export interface SolveStats {
  nodesVisited: number;
  backjumps: Backjump[];
}

export interface SolveResult {
  readonly assignment: readonly Value[] | null;
  readonly stats: SolveStats;
  readonly conflictSets: ReadonlyArray<readonly number[]>;
}

function buildAdjacency(problem: CspProblem): Map<number, BinaryConstraint[]>[] {
  const adj: Map<number, BinaryConstraint[]>[] = Array.from(
    { length: problem.numVars },
    () => new Map(),
  );
  for (const c of problem.constraints) {
    adj[c.a].set(c.b, [...(adj[c.a].get(c.b) ?? []), c]);
    adj[c.b].set(c.a, [...(adj[c.b].get(c.a) ?? []), c]);
  }
  return adj;
}

function consistentWithPast(
  level: number,
  value: Value,
  assignment: Value[],
  adj: Map<number, BinaryConstraint[]>[],
): { ok: boolean; culprit: number } {
  for (const [past, constraints] of adj[level]) {
    if (past >= level) continue;
    for (const c of constraints) {
      const va = c.a === level ? value : assignment[c.a];
      const vb = c.b === level ? value : assignment[c.b];
      if (!c.check(va, vb)) return { ok: false, culprit: past };
    }
  }
  return { ok: true, culprit: -1 };
}

/**
 * Conflict-directed Backjumping (Prosser, 1993).
 * 每层维护 conflict set：使该层取值失败的历史变量集合。
 * 域耗尽时跳回 conflict set 中最深的变量，并把其余冲突合并过去。
 */
export function solveCBJ(problem: CspProblem): SolveResult {
  const n = problem.numVars;
  const adj = buildAdjacency(problem);
  const assignment: Value[] = new Array(n);
  const remaining: Value[][] = problem.domains.map((d) => [...d]);
  const confSets: Set<number>[] = Array.from({ length: n }, () => new Set());
  const stats: SolveStats = { nodesVisited: 0, backjumps: [] };

  let level = 0;
  while (level >= 0 && level < n) {
    let assigned = false;
    while (remaining[level].length > 0) {
      const value = remaining[level][0];
      stats.nodesVisited++;
      const { ok, culprit } = consistentWithPast(level, value, assignment, adj);
      if (ok) {
        assignment[level] = value;
        assigned = true;
        break;
      }
      confSets[level].add(culprit);
      remaining[level].shift();
    }

    if (assigned) {
      level++;
      if (level < n) {
        remaining[level] = [...problem.domains[level]];
        confSets[level] = new Set();
      }
      continue;
    }

    const conf = confSets[level];
    if (conf.size === 0) {
      return {
        assignment: null,
        stats,
        conflictSets: confSets.map((s) => [...s].sort((x, y) => x - y)),
      };
    }
    const target = Math.max(...conf);
    stats.backjumps.push({
      fromLevel: level,
      toLevel: target,
      conflictSet: [...conf].sort((x, y) => x - y),
    });
    for (const other of conf) {
      if (other !== target) confSets[target].add(other);
    }
    for (let k = target + 1; k <= level; k++) {
      remaining[k] = [...problem.domains[k]];
      confSets[k] = new Set();
    }
    remaining[target].shift();
    level = target;
  }

  return {
    assignment: level === n ? [...assignment] : null,
    stats,
    conflictSets: confSets.map((s) => [...s].sort((x, y) => x - y)),
  };
}

/** 传统时序回溯（chronological backtracking），用于对照实验。 */
export function solveChronological(problem: CspProblem): SolveResult {
  const n = problem.numVars;
  const adj = buildAdjacency(problem);
  const assignment: Value[] = new Array(n);
  const remaining: Value[][] = problem.domains.map((d) => [...d]);
  const stats: SolveStats = { nodesVisited: 0, backjumps: [] };

  let level = 0;
  while (level >= 0 && level < n) {
    let assigned = false;
    while (remaining[level].length > 0) {
      const value = remaining[level][0];
      stats.nodesVisited++;
      if (consistentWithPast(level, value, assignment, adj).ok) {
        assignment[level] = value;
        assigned = true;
        break;
      }
      remaining[level].shift();
    }
    if (assigned) {
      level++;
      if (level < n) remaining[level] = [...problem.domains[level]];
    } else {
      remaining[level] = [...problem.domains[level]];
      level--;
      if (level >= 0) remaining[level].shift();
    }
  }

  return {
    assignment: level === n ? [...assignment] : null,
    stats,
    conflictSets: [],
  };
}

export function varName(problem: CspProblem, i: number): string {
  return problem.varNames?.[i] ?? `X${i + 1}`;
}
