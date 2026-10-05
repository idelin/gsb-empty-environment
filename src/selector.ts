/**
 * 选择器：在冲突约束、前置闭包约束和数量上限（默认 25）下，
 * 求总收益最大的兼容修复子集。
 *
 * 模型：以建议为点、冲突为边的最大权独立集，附加约束
 *  “选中 v 必须同时选中 requires 闭包 closure(v)”。
 * 求解：分支限界（按收益降序、剩余收益和上界剪枝、数量槽位剪枝），
 * 先用贪心构造下界；超出节点预算时返回当前最优（exact=false）。
 */
import { ConflictEvidence, FixSuggestion, RejectionReason } from './types';

export interface SelectionOutcome {
  selectedIds: string[];
  exact: boolean;
  /** 因前置关系结构性不可行的建议（未知前置、环、闭包内部冲突）。 */
  infeasible: Map<string, RejectionReason[]>;
}

const DEFAULT_NODE_BUDGET = 1_000_000;

export function selectMaxBenefit(
  suggestions: FixSuggestion[],
  conflicts: Map<string, ConflictEvidence[]>,
  maxCount: number,
  nodeBudget: number = DEFAULT_NODE_BUDGET,
): SelectionOutcome {
  const infeasible = new Map<string, RejectionReason[]>();
  const pushInfeasible = (id: string, reason: RejectionReason): void => {
    const list = infeasible.get(id) ?? [];
    list.push(reason);
    infeasible.set(id, list);
  };

  const byId = new Map(suggestions.map((s) => [s.id, s]));

  // ---- 1. 未知前置 ----
  for (const s of suggestions) {
    for (const req of s.requires ?? []) {
      if (!byId.has(req)) {
        pushInfeasible(s.id, { kind: 'unknown-prerequisite', prerequisiteId: req });
      }
    }
  }

  // ---- 2. 前置环检测（DFS 三色标记）----
  const color = new Map<string, 0 | 1 | 2>();
  const stack: string[] = [];
  const dfsCycle = (id: string): void => {
    if (infeasible.has(id) || color.get(id) !== undefined) return;
    color.set(id, 1);
    stack.push(id);
    for (const req of byId.get(id)?.requires ?? []) {
      if (!byId.has(req)) continue;
      if (color.get(req) === 1) {
        const cycle = [...stack.slice(stack.indexOf(req)), req];
        for (const member of cycle) pushInfeasible(member, { kind: 'prerequisite-cycle', cycle });
      } else {
        dfsCycle(req);
      }
    }
    stack.pop();
    color.set(id, 2);
  };
  for (const s of suggestions) dfsCycle(s.id);

  // ---- 3. 前置传递闭包 + 闭包内部冲突检查 ----
  const conflictSet = new Map<string, Set<string>>();
  for (const [id, list] of conflicts) {
    conflictSet.set(id, new Set(list.map((c) => c.otherId)));
  }
  const closureCache = new Map<string, string[]>();
  const closureOf = (id: string): string[] => {
    const cached = closureCache.get(id);
    if (cached) return cached;
    const out = new Set<string>([id]);
    const queue = [id];
    while (queue.length > 0) {
      const cur = queue.pop() as string;
      for (const req of byId.get(cur)?.requires ?? []) {
        if (!out.has(req) && byId.has(req)) {
          out.add(req);
          queue.push(req);
        }
      }
    }
    const arr = [...out];
    closureCache.set(id, arr);
    return arr;
  };

  const feasible: FixSuggestion[] = [];
  for (const s of suggestions) {
    if (infeasible.has(s.id)) continue;
    const closure = closureOf(s.id);
    if (closure.some((member) => infeasible.has(member))) {
      const bad = closure.find((member) => infeasible.has(member)) as string;
      pushInfeasible(s.id, {
        kind: 'missing-prerequisite',
        prerequisiteId: bad,
        detail: `前置 ${bad} 本身不可行`,
      });
      continue;
    }
    let innerConflict: string | undefined;
    for (const x of closure) {
      for (const y of closure) {
        if (x < y && conflictSet.get(x)?.has(y)) {
          innerConflict = `前置闭包内 ${x} 与 ${y} 互相冲突`;
          break;
        }
      }
      if (innerConflict) break;
    }
    if (innerConflict) {
      pushInfeasible(s.id, { kind: 'prerequisite-conflict', detail: innerConflict });
      continue;
    }
    if (closure.length > maxCount) {
      pushInfeasible(s.id, {
        kind: 'prerequisite-conflict',
        detail: `前置闭包大小 ${closure.length} 超过数量上限 ${maxCount}`,
      });
      continue;
    }
    feasible.push(s);
  }

  // ---- 4. 分支限界求最大权兼容子集 ----
  const items = [...feasible].sort((a, b) => b.benefit - a.benefit || a.id.localeCompare(b.id));
  const n = items.length;
  const indexOf = new Map(items.map((s, i) => [s.id, i]));
  const closures: number[][] = items.map((s) => closureOf(s.id).map((id) => indexOf.get(id) as number));
  const conflictIdx: Set<number>[] = items.map(
    (s) => new Set([...(conflictSet.get(s.id) ?? [])].map((id) => indexOf.get(id)).filter((x): x is number => x !== undefined)),
  );

  const chosen = new Array<boolean>(n).fill(false);
  const excluded = new Array<boolean>(n).fill(false);
  const chosenList: number[] = [];
  let bestBenefit = 0;
  let bestSet: number[] = [];
  let nodes = 0;
  let exact = true;

  const compatibleWithChosen = (i: number): boolean => {
    for (const c of chosenList) if (conflictIdx[i].has(c)) return false;
    return true;
  };

  const tryInclude = (i: number): number[] | null => {
    // 返回需要新增的索引列表；不可行返回 null。
    const add: number[] = [];
    for (const c of closures[i]) {
      if (chosen[c]) continue;
      if (excluded[c]) return null;
      if (!compatibleWithChosen(c)) return null;
      add.push(c);
    }
    // 闭包内部已验证无冲突，新增项之间亦安全。
    if (chosenList.length + add.length > maxCount) return null;
    return add;
  };

  const greedyLowerBound = (): void => {
    let benefit = 0;
    const picked: number[] = [];
    for (let i = 0; i < n; i++) {
      const add: number[] = [];
      let ok = true;
      for (const c of closures[i]) {
        if (chosen[c]) continue;
        if (!compatibleWithChosen(c)) { ok = false; break; }
        add.push(c);
      }
      if (ok && chosenList.length + add.length <= maxCount) {
        for (const c of add) {
          chosen[c] = true;
          chosenList.push(c);
          benefit += items[c].benefit;
          picked.push(c);
        }
      }
    }
    bestBenefit = benefit;
    bestSet = [...picked];
    chosen.fill(false);
    chosenList.length = 0;
  };

  greedyLowerBound();

  const upperBound = (pos: number, currentBenefit: number): number => {
    let bound = currentBenefit;
    let slots = maxCount - chosenList.length;
    for (let j = pos; j < n && slots > 0; j++) {
      if (chosen[j] || excluded[j]) continue;
      if (!compatibleWithChosen(j)) continue;
      bound += items[j].benefit;
      slots -= 1;
    }
    return bound;
  };

  const search = (pos: number, currentBenefit: number): void => {
    if (!exact) return;
    nodes += 1;
    if (nodes > nodeBudget) {
      exact = false;
      return;
    }
    if (currentBenefit > bestBenefit) {
      bestBenefit = currentBenefit;
      bestSet = [...chosenList];
    }
    if (pos >= n || chosenList.length === maxCount) return;
    if (upperBound(pos, currentBenefit) <= bestBenefit) return;

    const i = pos;
    // 分支一：包含 i（连同其前置闭包）
    const add = tryInclude(i);
    if (add !== null) {
      for (const c of add) { chosen[c] = true; chosenList.push(c); }
      let gain = 0;
      for (const c of add) gain += items[c].benefit;
      search(pos + 1, currentBenefit + gain);
      for (const c of add) { chosen[c] = false; }
      chosenList.length -= add.length;
    }
    // 分支二：排除 i
    excluded[i] = true;
    search(pos + 1, currentBenefit);
    excluded[i] = false;
  };

  search(0, 0);

  return {
    selectedIds: bestSet.map((i) => items[i].id),
    exact,
    infeasible,
  };
}
