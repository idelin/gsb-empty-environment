/**
 * 应用器：把选中的修复按合法顺序应用到文件内容上。
 *
 * 两个关键职责：
 *  1. 偏移平移 —— 每条建议的编辑区间基于“原文”，前面已应用的修复
 *     会移动后续文本，因此应用前必须把偏移换算到当前内容；
 *  2. 应用后重检 —— 每应用一条修复，就重新检查所有尚未应用修复的
 *     前置条件；失效的修复及其依赖者被丢弃，并记录证据。
 */
import { ApplyContext, FixSuggestion, Precondition, RejectionReason } from './types';

export interface DroppedFix {
  id: string;
  reasons: RejectionReason[];
}

export interface ApplyOutcome {
  appliedIds: string[];
  dropped: DroppedFix[];
  finalFiles: Record<string, string>;
}

/** 单个文件内已应用编辑的偏移映射。 */
class OffsetMap {
  private edits: { start: number; end: number; delta: number }[] = [];

  /** 把原文偏移 pos 换算为当前内容中的偏移（已应用的编辑互不相交）。 */
  translate(pos: number): number {
    let out = pos;
    for (const e of this.edits) {
      if (pos >= e.end) out += e.delta;
    }
    return out;
  }

  record(start: number, end: number, delta: number): void {
    this.edits.push({ start, end, delta });
  }
}

function applyEdit(content: string, start: number, end: number, replacement: string): string {
  return content.slice(0, start) + replacement + content.slice(end);
}

/** 拓扑排序：requires 的前置先应用；其余按收益降序、id 字典序。 */
export function applicationOrder(suggestions: FixSuggestion[]): FixSuggestion[] {
  const byId = new Map(suggestions.map((s) => [s.id, s]));
  const selected = new Set(suggestions.map((s) => s.id));
  const depthCache = new Map<string, number>();
  const depth = (id: string, seen: Set<string>): number => {
    const cached = depthCache.get(id);
    if (cached !== undefined) return cached;
    if (seen.has(id)) return 0;
    seen.add(id);
    const s = byId.get(id);
    let d = 0;
    for (const req of s?.requires ?? []) {
      if (selected.has(req)) d = Math.max(d, 1 + depth(req, seen));
    }
    depthCache.set(id, d);
    return d;
  };
  return [...suggestions].sort((a, b) => {
    const da = depth(a.id, new Set());
    const db = depth(b.id, new Set());
    if (da !== db) return da - db; // 前置（深度小）先应用
    if (a.benefit !== b.benefit) return b.benefit - a.benefit;
    return a.id.localeCompare(b.id);
  });
}

/**
 * 按顺序应用修复，每次应用后重新检查剩余修复的前置条件。
 * @param files 原始文件内容（path -> content）
 * @param selected 求解器选中的建议（顺序无关，内部会拓扑排序）
 */
export function applyWithRecheck(
  files: Record<string, string>,
  selected: FixSuggestion[],
): ApplyOutcome {
  const current = new Map<string, string>(Object.entries(files));
  const ctx: ApplyContext = { getFile: (p) => current.get(p) };
  const offsetMaps = new Map<string, OffsetMap>();
  const mapFor = (file: string): OffsetMap => {
    let m = offsetMaps.get(file);
    if (!m) {
      m = new OffsetMap();
      offsetMaps.set(file, m);
    }
    return m;
  };

  const ordered = applicationOrder(selected);
  const byId = new Map(selected.map((s) => [s.id, s]));
  const appliedIds: string[] = [];
  const dropped = new Map<string, DroppedFix>();
  /** 每个文件最近被哪些已应用修复改过（用于证据定位）。 */
  const touchedBy = new Map<string, string[]>();

  const dependentsOf = (id: string): string[] => {
    // 选中集中（传递）依赖 id 的修复
    const out: string[] = [];
    for (const s of selected) {
      if (s.id === id || dropped.has(s.id)) continue;
      const closure = new Set<string>();
      const queue = [...(s.requires ?? [])];
      while (queue.length > 0) {
        const cur = queue.pop() as string;
        if (closure.has(cur)) continue;
        closure.add(cur);
        queue.push(...(byId.get(cur)?.requires ?? []));
      }
      if (closure.has(id)) out.push(s.id);
    }
    return out;
  };

  const dropFix = (id: string, reason: RejectionReason): void => {
    if (dropped.has(id)) return;
    dropped.set(id, { id, reasons: [reason] });
    for (const dep of dependentsOf(id)) {
      dropFix(dep, { kind: 'dependent-dropped', droppedPrerequisite: id });
    }
  };

  const preconditionsHold = (s: FixSuggestion): Precondition | null => {
    for (const pre of s.preconditions ?? []) {
      if (!pre.check(ctx)) return pre;
    }
    return null;
  };

  for (const fix of ordered) {
    if (dropped.has(fix.id)) continue;

    // 应用前重检：前面的应用可能已经破坏了本修复的前置条件。
    const failed = preconditionsHold(fix);
    if (failed) {
      const invalidatedBy = [...new Set((failed.files ?? []).flatMap((f) => touchedBy.get(f) ?? []))];
      dropFix(fix.id, {
        kind: 'post-apply-precondition-failed',
        precondition: failed.description,
        invalidatedBy,
      });
      continue;
    }

    // 应用编辑：同文件内按起点降序，偏移先换算到当前内容。
    const editsByFile = new Map<string, typeof fix.edits>();
    for (const e of fix.edits) {
      const list = editsByFile.get(e.file) ?? [];
      list.push(e);
      editsByFile.set(e.file, list);
    }
    for (const [file, edits] of editsByFile) {
      let content = current.get(file) ?? '';
      const map = mapFor(file);
      // 1) 用“之前修复”的映射把本修复的编辑换算到当前内容；
      // 2) 按换算后的起点降序应用（同一原文坐标系内降序互不干扰）；
      // 3) 最后把本修复的原文坐标编辑登记进映射，供后续修复换算。
      const translated = edits
        .map((e) => ({
          start: map.translate(e.start),
          end: map.translate(e.end),
          replacement: e.replacement,
        }))
        .sort((a, b) => b.start - a.start);
      for (const e of translated) {
        content = applyEdit(content, e.start, e.end, e.replacement);
      }
      for (const e of edits) {
        map.record(e.start, e.end, e.replacement.length - (e.end - e.start));
      }
      current.set(file, content);
      const list = touchedBy.get(file) ?? [];
      list.push(fix.id);
      touchedBy.set(file, list);
    }
    appliedIds.push(fix.id);
  }

  const finalFiles: Record<string, string> = {};
  for (const [k, v] of current) finalFiles[k] = v;
  return { appliedIds, dropped: [...dropped.values()], finalFiles };
}
