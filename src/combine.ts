/**
 * 组合管线：从修复建议到最大收益兼容修复集合的完整流程。
 *
 * 1. 初始前置条件检查（针对原始文件内容）；
 * 2. 冲突检测（文本重叠 + 符号读写 + 显式失效 + 互斥组）；
 * 3. 分支限界求解总收益最大的兼容子集（前置闭包闭合、数量 ≤ maxCount）；
 * 4. 按拓扑序应用，每次应用后重新检查剩余修复的前置条件；
 * 5. 为每条未采纳的建议生成冲突/前置证据。
 */
import { applyWithRecheck } from './applier';
import { detectConflicts } from './conflicts';
import { selectMaxBenefit } from './selector';
import {
  ApplyContext,
  CombineResult,
  ConflictEvidence,
  FixSuggestion,
  Rejection,
  RejectionReason,
} from './types';

export const DEFAULT_MAX_COUNT = 25;

export interface CombineOptions {
  maxCount?: number;
  /** 分支限界节点预算，超出后返回当前最优（exact=false）。 */
  nodeBudget?: number;
}

export function combineFixes(
  files: Record<string, string>,
  suggestions: FixSuggestion[],
  options: CombineOptions = {},
): CombineResult {
  const maxCount = options.maxCount ?? DEFAULT_MAX_COUNT;
  const rejections = new Map<string, RejectionReason[]>();
  const addReasons = (id: string, reasons: RejectionReason[]): void => {
    const list = rejections.get(id) ?? [];
    list.push(...reasons);
    rejections.set(id, list);
  };

  // ---- 1. 初始前置条件检查 ----
  const initialCtx: ApplyContext = { getFile: (p) => files[p] };
  const viable: FixSuggestion[] = [];
  for (const s of suggestions) {
    const failed = (s.preconditions ?? []).find((pre) => !pre.check(initialCtx));
    if (failed) {
      addReasons(s.id, [{ kind: 'initial-precondition-failed', precondition: failed.description }]);
    } else {
      viable.push(s);
    }
  }

  // ---- 2. 冲突检测 ----
  const conflicts = detectConflicts(viable);

  // ---- 3. 最大收益兼容子集 ----
  const selection = selectMaxBenefit(viable, conflicts, maxCount, options.nodeBudget ?? 1_000_000);
  for (const [id, reasons] of selection.infeasible) addReasons(id, reasons);

  // ---- 4. 应用 + 应用后重检 ----
  const selectedSet = new Set(selection.selectedIds);
  const selectedSuggestions = viable.filter((s) => selectedSet.has(s.id));
  const applied = applyWithRecheck(files, selectedSuggestions);
  for (const d of applied.dropped) addReasons(d.id, d.reasons);

  const appliedSet = new Set(applied.appliedIds);
  const byId = new Map(suggestions.map((s) => [s.id, s]));

  // ---- 5. 未入选建议的证据 ----
  for (const s of viable) {
    if (appliedSet.has(s.id) || rejections.has(s.id)) continue;
    const reasons: RejectionReason[] = [];

    // 5a. 与最终应用集合的冲突证据（含语义冲突，而非仅文本范围）
    const conflictEvidence: ConflictEvidence[] = (conflicts.get(s.id) ?? []).filter((c) =>
      appliedSet.has(c.otherId),
    );
    if (conflictEvidence.length > 0) {
      reasons.push({ kind: 'conflict', conflicts: conflictEvidence });
    }

    // 5b. 前置未被选中
    for (const req of s.requires ?? []) {
      if (!appliedSet.has(req)) {
        reasons.push({
          kind: 'missing-prerequisite',
          prerequisiteId: req,
          detail: `前置 ${req} 未被选中（${byId.get(req)?.description ?? '未知建议'}）`,
        });
      }
    }

    // 5c. 既无冲突也不缺前置：纯粹因收益/数量预算未入选
    if (reasons.length === 0) {
      reasons.push({
        kind: 'benefit-too-low',
        detail:
          `与已选集合不存在冲突，但在最多 ${maxCount} 项的预算下，纳入该建议（收益 ${s.benefit}）` +
          `无法使总收益超过当前最优（${applied.appliedIds.reduce((sum, id) => sum + (byId.get(id)?.benefit ?? 0), 0)}）`,
      });
    }
    addReasons(s.id, reasons);
  }

  // ---- 汇总 ----
  const order = new Map(applied.appliedIds.map((id, i) => [id, i]));
  const selected = applied.appliedIds
    .map((id) => byId.get(id))
    .filter((s): s is FixSuggestion => s !== undefined)
    .map((s) => ({
      id: s.id,
      description: s.description,
      benefit: s.benefit,
      order: order.get(s.id) as number,
    }));
  const totalBenefit = selected.reduce((sum, s) => sum + s.benefit, 0);

  const rejected: Rejection[] = [];
  for (const s of suggestions) {
    if (appliedSet.has(s.id)) continue;
    rejected.push({ id: s.id, description: s.description, reasons: rejections.get(s.id) ?? [] });
  }
  rejected.sort((a, b) => a.id.localeCompare(b.id));

  return { selected, totalBenefit, rejected, finalFiles: applied.finalFiles, exact: selection.exact };
}
