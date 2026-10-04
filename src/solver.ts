import type { FixSuggestion } from "./types.ts";

export interface SolveProblem {
  /** 候选建议（已按 id 去重、通过输入校验） */
  candidates: FixSuggestion[];
  /** hardConflicts[i] 为与 i 冲突的下标集合 */
  hardConflicts: Set<number>[];
  maxSelected: number;
  /** 搜索节点预算，超出后返回当前最优（optimal=false） */
  nodeBudget?: number;
}

export interface SolveOutcome {
  chosen: number[];
  totalBenefit: number;
  optimal: boolean;
}

/**
 * 分支定界求最大收益兼容子集：
 * - 冲突图上两两互斥；
 * - requires 蕴含：选 i 必须选其前置闭包；
 * - 基数不超过 maxSelected。
 */
export function solveMaxBenefit(problem: SolveProblem): SolveOutcome {
  const { candidates, hardConflicts, maxSelected } = problem;
  const n = candidates.length;
  const nodeBudget = problem.nodeBudget ?? 2_000_000;

  // 前置闭包（含自身）；存在循环或内部冲突时记为 null（不可选）
  const closures: (number[] | null)[] = new Array<number[] | null>(n).fill(null);
  const dependents: Set<number>[] = Array.from(
    { length: n },
    () => new Set<number>(),
  );
  const idToIndex = new Map<string, number>();
  candidates.forEach((s, i) => idToIndex.set(s.id, i));

  for (let i = 0; i < n; i++) {
    const closure = computeClosure(i, candidates, idToIndex, hardConflicts);
    closures[i] = closure;
  }
  for (let i = 0; i < n; i++) {
    for (const req of candidates[i]!.requires ?? []) {
      const j = idToIndex.get(req);
      if (j !== undefined) dependents[j]!.add(i);
    }
  }
  // 传递依赖者：排除 j 时需一并排除依赖 j 的所有节点
  const transitiveDependents: Set<number>[] = Array.from(
    { length: n },
    () => new Set<number>(),
  );
  for (let i = 0; i < n; i++) {
    const stack = [...dependents[i]!];
    while (stack.length > 0) {
      const d = stack.pop()!;
      if (transitiveDependents[i]!.has(d)) continue;
      transitiveDependents[i]!.add(d);
      for (const next of dependents[d]!) stack.push(next);
    }
  }

  // 按收益降序排列的访问顺序（稳定：收益相同按 id）
  const order = Array.from({ length: n }, (_, i) => i).sort((a, b) => {
    const diff = candidates[b]!.benefit - candidates[a]!.benefit;
    return diff !== 0 ? diff : candidates[a]!.id.localeCompare(candidates[b]!.id);
  });
  const sortedBenefits = candidates
    .map((c) => c.benefit)
    .sort((a, b) => b - a);
  // 上界前缀和：任意剩余选择最多再获得 prefixSum[k]
  const prefixSum: number[] = [0];
  for (let k = 0; k < Math.min(maxSelected, n); k++) {
    prefixSum.push(prefixSum[k]! + sortedBenefits[k]!);
  }

  const banned = new Array<boolean>(n).fill(false);
  // inChosen[i]：i 已作为某个闭包的成员被选入，访问到它时应跳过
  const inChosen = new Array<boolean>(n).fill(false);
  const chosen: number[] = [];
  let chosenBenefit = 0;
  let best: number[] = [];
  let bestBenefit = -1;
  let nodes = 0;
  let exhausted = true;

  function upperBound(remainingSlots: number): number {
    const k = Math.min(remainingSlots, prefixSum.length - 1);
    // 粗上界：忽略冲突与 banned，直接取全局收益前 k 名之和
    return chosenBenefit + prefixSum[k]!;
  }

  function dfs(pos: number): void {
    if (chosen.length > 0 && chosenBenefit > bestBenefit) {
      bestBenefit = chosenBenefit;
      best = [...chosen];
    }
    if (pos >= n || chosen.length >= maxSelected) return;
    if (++nodes > nodeBudget) {
      exhausted = false;
      return;
    }
    const remainingSlots = maxSelected - chosen.length;
    if (upperBound(remainingSlots) <= bestBenefit) return;

    const i = order[pos]!;
    const closure = closures[i];
    const canInclude =
      !banned[i] &&
      !inChosen[i] &&
      closure !== null &&
      chosen.length + closure.length <= maxSelected &&
      closure.every((c) => !banned[c] && !inChosen[c]);

    if (canInclude) {
      // 分支一：选 i（连同其前置闭包）
      const addedBanned: number[] = [];
      for (const c of closure!) {
        chosen.push(c);
        inChosen[c] = true;
        chosenBenefit += candidates[c]!.benefit;
        for (const x of hardConflicts[c]!) {
          if (!banned[x]) {
            banned[x] = true;
            addedBanned.push(x);
          }
        }
      }
      dfs(pos + 1);
      for (let k = 0; k < closure!.length; k++) {
        inChosen[closure![k]!] = false;
        chosen.pop();
        chosenBenefit -= candidates[closure![k]!]!.benefit;
      }
      for (const x of addedBanned) banned[x] = false;
      if (!exhausted) return;
    }

    // 分支二：不选 i —— 其传递依赖者也不可再选
    if (inChosen[i]) {
      // 已随其他闭包被选入：无需“排除”分支，直接前进
      dfs(pos + 1);
      return;
    }
    const addedBanned: number[] = [i];
    banned[i] = true;
    for (const d of transitiveDependents[i]!) {
      if (!banned[d]) {
        banned[d] = true;
        addedBanned.push(d);
      }
    }
    dfs(pos + 1);
    for (const x of addedBanned) banned[x] = false;
  }

  dfs(0);
  best.sort((a, b) => a - b);
  return { chosen: best, totalBenefit: Math.max(bestBenefit, 0), optimal: exhausted };
}

function computeClosure(
  start: number,
  candidates: FixSuggestion[],
  idToIndex: Map<string, number>,
  hardConflicts: Set<number>[],
): number[] | null {
  const members = new Set<number>();
  const stack = [start];
  while (stack.length > 0) {
    const i = stack.pop()!;
    if (members.has(i)) continue;
    members.add(i);
    for (const req of candidates[i]!.requires ?? []) {
      const j = idToIndex.get(req);
      if (j === undefined) return null; // 前置不存在：不可选
      // requires 环合法：环上节点构成同一闭包，必须整体取舍
      stack.push(j);
    }
  }
  // 闭包内部存在硬冲突则整体不可选
  for (const i of members) {
    for (const j of hardConflicts[i]!) {
      if (members.has(j)) return null;
    }
  }
  return [...members];
}
