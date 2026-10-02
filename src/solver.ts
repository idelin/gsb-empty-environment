import type { CspProblem, Value } from "./csp.ts";

export type SolveMode = "cbj" | "chronological";

export type TraceEvent =
  | { type: "assign"; level: number; variable: string; value: Value }
  | {
      type: "reject";
      level: number;
      variable: string;
      value: Value;
      blamedLevel: number;
      blamedVariable: string;
      constraint: string;
    }
  | { type: "dead-end"; level: number; variable: string; conflictSet: number[] }
  | { type: "backjump"; from: number; to: number; width: number }
  | { type: "solution"; assignment: Value[] }
  | { type: "unsat" };

export interface BackjumpRecord {
  from: number;
  to: number;
  width: number;
}

export interface SolveStats {
  /** 尝试赋值的次数（搜索节点数） */
  nodes: number;
  /** 约束一致性检查次数 */
  constraintChecks: number;
  /** 死胡同次数 */
  deadEnds: number;
  /** 发生的回跳记录（from/to 为 1 基层级） */
  backjumps: BackjumpRecord[];
}

export interface SolveResult {
  solution: Value[] | null;
  stats: SolveStats;
  events: TraceEvent[];
  /** 每个变量最终的冲突集（变量下标集合，下标为 0 基） */
  conflictSets: Set<number>[];
}

/**
 * 冲突导向回跳（Conflict-directed Backjumping, Prosser 1993）CSP 求解器。
 *
 * 按固定顺序实例化变量；为当前变量试值时与所有已赋值变量做一致性检查，
 * 每次冲突都把"肇事"的过去变量记入当前变量的冲突集。当前变量值域耗尽时，
 * 不回退到上一变量，而是跳到冲突集中最深的变量，并把冲突集（去掉跳点）
 * 合并过去，从而跳过与矛盾无关的中间变量。
 *
 * mode = "chronological" 时退化为传统时序回溯，用于对照实验。
 */
export function solve(problem: CspProblem, mode: SolveMode = "cbj"): SolveResult {
  const n = problem.varNames.length;
  const constraintsByVar: typeof problem.constraints[] = Array.from(
    { length: n },
    () => [],
  );
  for (const c of problem.constraints) {
    constraintsByVar[c.a].push(c);
    constraintsByVar[c.b].push(c);
  }

  const assignment: (Value | undefined)[] = new Array(n).fill(undefined);
  const remaining: Value[][] = problem.domains.map((d) => [...d]);
  const conflictSets: Set<number>[] = Array.from({ length: n }, () => new Set());

  const stats: SolveStats = {
    nodes: 0,
    constraintChecks: 0,
    deadEnds: 0,
    backjumps: [],
  };
  const events: TraceEvent[] = [];

  /** 返回与 (i, value) 冲突的过去变量下标；无冲突返回 -1。 */
  function firstConflict(i: number, value: Value): { past: number; name: string } {
    for (const c of constraintsByVar[i]) {
      const past = c.a === i ? c.b : c.a;
      if (past >= i || assignment[past] === undefined) continue;
      stats.constraintChecks++;
      const ok =
        c.a === i
          ? c.holds(value, assignment[past] as Value)
          : c.holds(assignment[past] as Value, value);
      if (!ok) return { past, name: c.name };
    }
    return { past: -1, name: "" };
  }

  let i = 0;
  let solution: Value[] | null = null;

  while (i >= 0 && i < n) {
    const next = remaining[i].shift();
    if (next !== undefined) {
      stats.nodes++;
      const { past, name } = firstConflict(i, next);
      if (past === -1) {
        assignment[i] = next;
        events.push({
          type: "assign",
          level: i + 1,
          variable: problem.varNames[i],
          value: next,
        });
        i++;
      } else {
        conflictSets[i].add(past);
        events.push({
          type: "reject",
          level: i + 1,
          variable: problem.varNames[i],
          value: next,
          blamedLevel: past + 1,
          blamedVariable: problem.varNames[past],
          constraint: name,
        });
      }
      continue;
    }

    // 值域耗尽：死胡同，决定回跳目标
    stats.deadEnds++;
    const conflict = [...conflictSets[i]].sort((a, b) => a - b);
    events.push({
      type: "dead-end",
      level: i + 1,
      variable: problem.varNames[i],
      conflictSet: conflict.map((x) => x + 1),
    });

    const target =
      mode === "cbj"
        ? conflict.length === 0
          ? -1
          : conflict[conflict.length - 1]
        : i - 1;

    if (target < 0) {
      events.push({ type: "unsat" });
      return { solution: null, stats, events, conflictSets };
    }

    // 冲突集回传：把 conf[i] \ {target} 合并进 conf[target]
    for (const h of conflictSets[i]) {
      if (h !== target) conflictSets[target].add(h);
    }

    // 撤销 target+1 .. i 的赋值并重置其状态
    for (let k = target + 1; k <= i; k++) {
      assignment[k] = undefined;
      remaining[k] = [...problem.domains[k]];
      conflictSets[k].clear();
    }
    assignment[target] = undefined;

    stats.backjumps.push({ from: i + 1, to: target + 1, width: i - target });
    events.push({ type: "backjump", from: i + 1, to: target + 1, width: i - target });
    i = target;
  }

  if (i === n) {
    solution = assignment as Value[];
    events.push({ type: "solution", assignment: [...solution] });
  } else {
    events.push({ type: "unsat" });
  }
  return { solution, stats, events, conflictSets };
}
