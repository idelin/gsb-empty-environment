import type {
  CombineInput,
  CombineResult,
  ConflictEvidence,
  FileState,
  FixSuggestion,
  RejectedSuggestion,
  RejectReason,
} from "./types.ts";
import { analyzePair, writesInterfereWithReads } from "./conflicts.ts";
import { evaluatePrecondition } from "./preconditions.ts";
import { applyEdits } from "./apply.ts";
import { editToOffsets } from "./ranges.ts";
import { solveMaxBenefit } from "./solver.ts";

export const DEFAULT_MAX_SELECTED = 25;
const MAX_ITERATIONS = 10;

/** 输入校验：返回每个建议的非法原因（空数组表示合法） */
function validateSuggestion(
  s: FixSuggestion,
  files: FileState,
  knownIds: Set<string>,
): string[] {
  const problems: string[] = [];
  if (!Number.isFinite(s.benefit) || s.benefit < 0) {
    problems.push(`benefit 必须为非负有限数，实际为 ${s.benefit}`);
  }
  for (const edit of s.edits) {
    const content = files[edit.file];
    if (content === undefined) {
      problems.push(`编辑指向不存在的文件 ${edit.file}`);
      continue;
    }
    try {
      editToOffsets(content, edit);
    } catch (err) {
      problems.push(`编辑范围非法（${edit.file}）：${(err as Error).message}`);
    }
  }
  for (const req of s.requires ?? []) {
    if (!knownIds.has(req)) {
      problems.push(`requires 引用了不存在的建议 ${req}`);
    }
  }
  return problems;
}

/** 对选中集合按 requires 拓扑排序（环内按 id 排序），保证前置先应用 */
function applicationOrder(selected: FixSuggestion[]): FixSuggestion[] {
  const byId = new Map(selected.map((s) => [s.id, s]));
  const inDegree = new Map<string, number>();
  const edges = new Map<string, string[]>();
  for (const s of selected) {
    inDegree.set(s.id, 0);
  }
  for (const s of selected) {
    for (const req of s.requires ?? []) {
      if (!byId.has(req)) continue;
      inDegree.set(s.id, (inDegree.get(s.id) ?? 0) + 1);
      const list = edges.get(req) ?? [];
      list.push(s.id);
      edges.set(req, list);
    }
  }
  const ready = [...inDegree.entries()]
    .filter(([, d]) => d === 0)
    .map(([id]) => id)
    .sort();
  const ordered: FixSuggestion[] = [];
  const seen = new Set<string>();
  while (ready.length > 0) {
    const id = ready.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    ordered.push(byId.get(id)!);
    for (const next of edges.get(id) ?? []) {
      const d = inDegree.get(next)! - 1;
      inDegree.set(next, d);
      if (d === 0) {
        ready.push(next);
        ready.sort();
      }
    }
  }
  // 环中剩余节点按 id 追加
  for (const s of [...selected].sort((a, b) => a.id.localeCompare(b.id))) {
    if (!seen.has(s.id)) ordered.push(s);
  }
  return ordered;
}

interface SimulationFailure {
  id: string;
  details: string[];
  invalidatedBy: string[];
}

/**
 * 模拟按顺序应用所选修复：每应用一个之前，先在当前文件状态下
 * 重新检查其前置条件；不满足则记录失败并跳过（不应用其编辑）。
 * 暂不满足的修复会被延迟，在后续修复应用后重试，
 * 直到一轮下来没有任何修复可应用（不动点），剩余的才记为失败。
 */
function simulate(
  ordered: FixSuggestion[],
  files: FileState,
): { state: FileState; failures: SimulationFailure[] } {
  let state = files;
  const applied: FixSuggestion[] = [];
  const pending = [...ordered];
  let progressed = true;
  while (progressed && pending.length > 0) {
    progressed = false;
    for (let i = 0; i < pending.length; i++) {
      const fix = pending[i]!;
      const ok = fix.preconditions.every(
        (pc) => evaluatePrecondition(pc, state).ok,
      );
      if (!ok) continue;
      state = applyEdits(state, fix.edits);
      applied.push(fix);
      pending.splice(i, 1);
      i--;
      progressed = true;
    }
  }
  const failures: SimulationFailure[] = [];
  for (const fix of pending) {
    const failedDetails: string[] = [];
    for (const pc of fix.preconditions) {
      const verdict = evaluatePrecondition(pc, state);
      if (!verdict.ok) failedDetails.push(verdict.detail);
    }
    const invalidatedBy = new Set<string>();
    for (const prev of applied) {
      if (writesInterfereWithReads(prev, fix).length > 0) {
        invalidatedBy.add(prev.id);
      }
    }
    failures.push({
      id: fix.id,
      details: failedDetails,
      invalidatedBy: [...invalidatedBy].sort(),
    });
  }
  return { state, failures };
}

export function selectCompatibleFixes(input: CombineInput): CombineResult {
  const maxSelected = input.maxSelected ?? DEFAULT_MAX_SELECTED;
  const files = input.files;

  // 1. 校验与去重
  const seen = new Set<string>();
  const rejected = new Map<string, RejectedSuggestion>();
  const deduped: FixSuggestion[] = [];
  for (const s of input.suggestions) {
    if (seen.has(s.id)) {
      rejected.set(s.id, {
        id: s.id,
        description: s.description,
        benefit: s.benefit,
        reasons: [{ kind: "invalid", detail: `重复的建议 id: ${s.id}` }],
      });
      continue;
    }
    seen.add(s.id);
    deduped.push(s);
  }
  const candidates: FixSuggestion[] = [];
  for (const s of deduped) {
    const problems = validateSuggestion(s, files, seen);
    if (problems.length > 0) {
      rejected.set(s.id, {
        id: s.id,
        description: s.description,
        benefit: s.benefit,
        reasons: problems.map((detail) => ({ kind: "invalid", detail })),
      });
      continue;
    }
    candidates.push(s);
  }

  // 2. 两两硬冲突分析（编辑重叠 + 语义冲突标签）
  const hardConflicts: Set<number>[] = candidates.map(() => new Set<number>());
  const pairEvidence = new Map<string, ConflictEvidence[]>();
  for (let i = 0; i < candidates.length; i++) {
    for (let j = i + 1; j < candidates.length; j++) {
      const evidence = analyzePair(candidates[i]!, candidates[j]!, files);
      if (evidence.length > 0) {
        hardConflicts[i]!.add(j);
        hardConflicts[j]!.add(i);
        pairEvidence.set(`${i}:${j}`, evidence);
        pairEvidence.set(
          `${j}:${i}`,
          evidence.map((e) => ({ ...e, otherId: candidates[i]!.id })),
        );
      }
    }
  }

  // 3. 求解 → 模拟应用并复查前置条件 → 失败则剔除后重解
  let pool = candidates.map((_, i) => i);
  let chosen: number[] = [];
  let optimal = true;
  const removedAll = new Set<number>();
  for (let iter = 0; iter < MAX_ITERATIONS; iter++) {
    const poolCandidates = pool.map((i) => candidates[i]!);
    const indexRemap = new Map<number, number>();
    pool.forEach((orig, idx) => indexRemap.set(idx, orig));
    const origToPool = new Map<number, number>();
    pool.forEach((orig, idx) => origToPool.set(orig, idx));
    const poolConflicts: Set<number>[] = poolCandidates.map(
      () => new Set<number>(),
    );
    for (let a = 0; a < pool.length; a++) {
      for (const b of hardConflicts[pool[a]!]!) {
        const bIdx = origToPool.get(b);
        if (bIdx !== undefined) poolConflicts[a]!.add(bIdx);
      }
    }
    const outcome = solveMaxBenefit({
      candidates: poolCandidates,
      hardConflicts: poolConflicts,
      maxSelected,
    });
    if (!outcome.optimal) optimal = false;
    chosen = outcome.chosen.map((idx) => indexRemap.get(idx)!);
    const ordered = applicationOrder(chosen.map((i) => candidates[i]!));
    const { state, failures } = simulate(ordered, files);
    if (failures.length === 0) break;

    // 前置条件在应用后被破坏：剔除失败者及其依赖者，记录证据后重解
    const failedIds = new Set(failures.map((f) => f.id));
    const removed = new Set<number>();
    for (const failure of failures) {
      const idx = candidates.findIndex((c) => c.id === failure.id);
      if (idx >= 0) removed.add(idx);
      const s = candidates[idx]!;
      rejected.set(s.id, {
        id: s.id,
        description: s.description,
        benefit: s.benefit,
        reasons: [
          {
            kind: "precondition-failed-after-apply",
            detail: failure.details.join("；"),
            invalidatedBy: failure.invalidatedBy,
          },
        ],
      });
    }
    // 依赖失败者的建议一并剔除
    let changed = true;
    while (changed) {
      changed = false;
      for (const i of chosen) {
        if (removed.has(i)) continue;
        const s = candidates[i]!;
        for (const req of s.requires ?? []) {
          if (failedIds.has(req) || [...removed].some((r) => candidates[r]!.id === req)) {
            removed.add(i);
            rejected.set(s.id, {
              id: s.id,
              description: s.description,
              benefit: s.benefit,
              reasons: [
                {
                  kind: "requires-excluded",
                  requiredId: req,
                  chain: [req, s.id],
                  detail: `前置修复 ${req} 因前置条件失效被移除`,
                },
              ],
            });
            changed = true;
            break;
          }
        }
      }
    }
    pool = pool.filter((i) => !removed.has(i));
    for (const i of removed) removedAll.add(i);
  }
  // 达到迭代上限时，剔除所有已移除项后重新模拟，保证输出状态一致
  chosen = chosen.filter((i) => !removedAll.has(i));
  const finalState = simulate(
    applicationOrder(chosen.map((i) => candidates[i]!)),
    files,
  ).state;

  const selectedSet = new Set(chosen.map((i) => candidates[i]!.id));
  const selectedIdx = new Set(chosen);

  // 4. 为其余未选中建议生成冲突证据
  for (let i = 0; i < candidates.length; i++) {
    if (selectedIdx.has(i) || rejected.has(candidates[i]!.id)) continue;
    const s = candidates[i]!;
    const reasons: RejectReason[] = [];

    const evidence: ConflictEvidence[] = [];
    for (const j of selectedIdx) {
      const ev = pairEvidence.get(`${i}:${j}`);
      if (ev) evidence.push(...ev);
    }
    if (evidence.length > 0) reasons.push({ kind: "conflict", evidence });

    for (const req of s.requires ?? []) {
      if (!selectedSet.has(req)) {
        const reqRejected = rejected.get(req);
        reasons.push({
          kind: "requires-excluded",
          requiredId: req,
          chain: [req, s.id],
          detail: reqRejected
            ? `前置修复 ${req} 未被选中（${reqRejected.reasons[0]?.kind ?? "未知原因"}）`
            : `前置修复 ${req} 未被选中`,
        });
      }
    }

    if (reasons.length === 0) {
      reasons.push({
        kind: "capacity",
        detail:
          `与已选集合无直接冲突，但在最多 ${maxSelected} 项的约束下，`
          + `选入它（收益 ${s.benefit}）不能提升总收益`,
      });
    }
    rejected.set(s.id, {
      id: s.id,
      description: s.description,
      benefit: s.benefit,
      reasons,
    });
  }

  const selected = chosen.map((i) => {
    const s = candidates[i]!;
    return { id: s.id, description: s.description, benefit: s.benefit };
  });
  const totalBenefit = selected.reduce((sum, s) => sum + s.benefit, 0);
  return {
    selected,
    totalBenefit,
    rejected: [...rejected.values()],
    files: finalState,
    optimal,
  };
}
