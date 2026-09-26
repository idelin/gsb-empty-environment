import type {
  CandidateGroup,
  ChosenPatch,
  JsonObject,
  PatchCandidate,
  RepairPlan,
  RepairSuggestionResult,
  RootSchema,
  ValidationError,
} from './types.ts';
import { errorKey } from './candidates.ts';
import { deepClone, getAtPath, setAtPath, unsetAtPath } from './paths.ts';
import { validateInstance } from './validate.ts';

const MAX_TIES = 64;

export interface SearchOptions {
  budget: number; // 允许核验的组合数上限（有限实例穷举预算）
}

interface UndoEntry {
  path: string;
  existed: boolean;
  previous: unknown;
}

function applyCandidate(
  root: JsonObject,
  cand: PatchCandidate,
): { ok: boolean; reason?: string; undo?: UndoEntry } {
  const cur = getAtPath(root, cand.targetPath);
  if (cand.preconditionValue.present) {
    if (!cur.exists) {
      return {
        ok: false,
        reason: `前置值不满足：期望路径存在且等于 ${JSON.stringify(cand.preconditionValue.value)}，实际路径缺失`,
      };
    }
    if (JSON.stringify(cur.value) !== JSON.stringify(cand.preconditionValue.value)) {
      return {
        ok: false,
        reason: `前置值不满足：当前值 ${JSON.stringify(cur.value)} 已被其它补丁改变，不再等于 ${JSON.stringify(
          cand.preconditionValue.value,
        )}`,
      };
    }
  } else if (cur.exists) {
    return { ok: false, reason: '前置值不满足：期望路径缺失，但路径已存在（可能被其它补丁创建）' };
  }

  const undo: UndoEntry = { path: cand.targetPath, existed: cur.exists, previous: cur.value };
  if (cand.op === 'unset') {
    unsetAtPath(root, cand.targetPath);
  } else {
    setAtPath(root, cand.targetPath, cand.value);
  }
  return { ok: true, undo };
}

function rollbackOne(root: JsonObject, undo: UndoEntry): void {
  if (undo.existed) {
    setAtPath(root, undo.path, undo.previous);
  } else {
    unsetAtPath(root, undo.path);
  }
}

function isErrorCoveredByPending(err: ValidationError, pendingPaths: string[]): boolean {
  const p = err.instancePath;
  for (const t of pendingPaths) {
    if (p === t || p.startsWith(t + '/')) return true;
    if (err.otherPath && (err.otherPath === t || err.otherPath.startsWith(t + '/'))) return true;
  }
  return false;
}

function toChosen(c: PatchCandidate): ChosenPatch {
  return {
    targetPath: c.targetPath,
    op: c.op,
    ...(c.op === 'set' && c.value !== undefined ? { value: c.value } : {}),
    certainty: c.certainty,
    cost: c.cost,
    originRulePath: c.originRulePath,
    reason: c.reason,
    ...(c.pending ? { pending: true } : {}),
  };
}

function leafSignature(selected: PatchCandidate[]): string {
  return JSON.stringify(
    selected
      .map((c) => `${c.targetPath}:${c.op}:${c.value ?? ''}:${c.pending ? 1 : 0}`)
      .sort(),
  );
}

interface BestState {
  legal: { cost: number; plans: RepairPlan[] } | null;
  pending: { cost: number; plans: RepairPlan[] } | null;
}

export function searchRepairs(
  originalInstance: unknown,
  schema: RootSchema,
  initialErrors: ValidationError[],
  groups: CandidateGroup[],
  opts: SearchOptions,
): RepairSuggestionResult {
  const errorKeys = new Set(initialErrors.map(errorKey));
  const work = deepClone(originalInstance as JsonObject);

  const combinationsTotal = groups.reduce(
    (acc, g) => acc * BigInt(g.candidates.length + 1),
    1n,
  );

  const best: BestState = { legal: null, pending: null };
  let checked = 0;
  let aborted = false;
  const seenLeaves = new Set<string>();

  // 分支定界：组内候选按代价升序；组间按最小代价降序，尽快拿到紧上界
  const orderedGroups = groups
    .map((g) => ({
      ...g,
      candidates: [...g.candidates].sort((a, b) => a.cost - b.cost),
    }))
    .sort((a, b) => a.candidates[0].cost - b.candidates[0].cost);

  function considerPlan(selected: PatchCandidate[]): void {
    const totalCost = selected.reduce((s, c) => s + c.cost, 0);
    const pendingPaths = selected.filter((c) => c.pending).map((c) => c.targetPath);
    const containsPending = pendingPaths.length > 0;

    const result = validateInstance(work, schema);
    let residual: ValidationError[];
    if (containsPending) {
      residual = result.errors.filter((e) => !isErrorCoveredByPending(e, pendingPaths));
    } else {
      residual = result.errors;
    }
    // 残留错误中不允许还出现初始错误（必须把每个报错都处理掉，不能只修最先报错字段）
    const residualInitial = residual.filter((e) => errorKeys.has(errorKey(e)));
    if (residual.length > 0 || residualInitial.length > 0) return;
    const plan: RepairPlan = {
      patches: selected
        .slice()
        .sort((a, b) => a.targetPath.localeCompare(b.targetPath))
        .map(toChosen),
      totalCost,
      allCertain: selected.every((c) => c.certainty === 'certain'),
      containsPending,
      status: containsPending ? 'pending-human' : 'legal',
    };

    const tier = containsPending ? best.pending : best.legal;
    const slot: 'legal' | 'pending' = containsPending ? 'pending' : 'legal';
    if (!tier || totalCost < tier.cost) {
      best[slot] = { cost: totalCost, plans: [plan] };
    } else if (totalCost === tier.cost && tier.plans.length < MAX_TIES) {
      const sig = JSON.stringify(
        plan.patches.map((q) => [q.targetPath, q.op, q.value ?? null, q.pending ? 'P' : '']),
      );
      if (!tier.plans.some((p2) => JSON.stringify(
        p2.patches.map((q) => [q.targetPath, q.op, q.value ?? null, q.pending ? 'P' : '']),
      ) === sig)) {
        tier.plans.push(plan);
      }
    }
  }

  // 穷举前先用廉价贪心找到一个可行解，保证预算中断时也能返回“可行上界”
  function greedySeed(): PatchCandidate[] | null {
    const picks: PatchCandidate[] = [];
    const undos: UndoEntry[] = [];
    let remaining = new Set(initialErrors.map(errorKey));
    for (const group of orderedGroups) {
      const cand = group.candidates[0]; // 已按代价升序
      const r = applyCandidate(work, cand);
      if (!r.ok) continue;
      const result = validateInstance(work, schema);
      const after = new Set(result.errors.map(errorKey));
      const reduced = [...remaining].some((k) => !after.has(k));
      if (reduced || after.size < remaining.size || (after.size === 0 && result.valid)) {
        picks.push(cand);
        if (r.undo) undos.push(r.undo);
        remaining = after;
        if (result.valid) break;
      } else if (r.undo) {
        rollbackOne(work, r.undo);
      }
    }
    const finalCheck = validateInstance(work, schema);
    if (!finalCheck.valid) {
      // 贪心失败，撤销其全部尝试
      for (let i = undos.length - 1; i >= 0; i--) rollbackOne(work, undos[i]);
      return null;
    }
    // 保留在 work 上的状态供 considerPlan 记录，然后立即撤销回到干净输入
    const seed = [...picks];
    for (let i = undos.length - 1; i >= 0; i--) rollbackOne(work, undos[i]);
    return seed;
  }

  function dfs(idx: number, accCost: number, selected: PatchCandidate[]): void {
    if (aborted) return;
    if (accCost > (best.legal?.cost ?? Infinity)) return; // 分支定界
    if (idx === orderedGroups.length) {
      const sig = leafSignature(selected);
      if (seenLeaves.has(sig)) return;
      seenLeaves.add(sig);
      checked += 1;
      if (checked > opts.budget) {
        aborted = true;
        return;
      }
      considerPlan(selected);
      return;
    }

    const group = orderedGroups[idx];

    // 选择一：不改该路径（可能仍有错误，叶节点核验）
    dfs(idx + 1, accCost, selected);

    // 选择二..：该组内互斥候选
    for (const cand of group.candidates) {
      if (aborted) return;
      if (accCost + cand.cost > (best.legal?.cost ?? Infinity)) continue;
      const r = applyCandidate(work, cand);
      if (!r.ok) continue; // 前置值不满足的组合在当前状态下不可行
      selected.push(cand);
      dfs(idx + 1, accCost + cand.cost, selected);
      selected.pop();
      if (r.undo) rollbackOne(work, r.undo);
    }
  }

  const seedPicks = greedySeed();
  if (seedPicks) {
    seenLeaves.add(leafSignature(seedPicks));
    checked += 1;
    // 贪心解可能含 pending；在干净 work 上重放以走同一判定流程
    const undos: UndoEntry[] = [];
    for (const cand of seedPicks) {
      const r = applyCandidate(work, cand);
      if (r.ok && r.undo) undos.push(r.undo);
    }
    considerPlan(seedPicks);
    for (let i = undos.length - 1; i >= 0; i--) rollbackOne(work, undos[i]);
  }

  dfs(0, 0, []);

  const chosenTier = best.legal ?? best.pending;
  // 预算中断时，穷举未完成：所有候选组都可能存在未核验组合，全部列为未决
  const unexplored: string[] = aborted ? orderedGroups.map((g) => g.targetPath) : [];

  const pendingGroups = chosenTier
    ? Array.from(
        new Set(
          chosenTier.plans.flatMap((p) =>
            p.patches.filter((q) => q.pending).map((q) => q.targetPath),
          ),
        ),
      ).sort()
    : [];

  let status: RepairSuggestionResult['status'];
  if (!aborted) {
    if (best.legal) status = 'optimal-proven';
    else if (best.pending) status = 'optimal-pending-human';
    else status = 'no-candidate';
  } else {
    status = 'budget-exhausted';
  }

  return {
    valid: false,
    errors: initialErrors,
    status,
    groups,
    bestPlans: chosenTier ? chosenTier.plans : [],
    optimalCost: aborted ? null : chosenTier ? chosenTier.cost : null,
    upperBoundCost: chosenTier ? chosenTier.cost : null,
    pendingGroups,
    unexploredGroups: unexplored,
    budget: { used: checked, limit: opts.budget },
    combinationsTotal: combinationsTotal.toString(),
    combinationsChecked: checked,
  };
}
