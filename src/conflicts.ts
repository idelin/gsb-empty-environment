/**
 * 冲突检测：判断两条修复建议为什么不能同时应用。
 *
 * 文本范围不重叠只是必要条件而非充分条件，因此这里同时检测：
 *  1. 文本重叠（同文件编辑区间相交，或同一位置的双重插入）；
 *  2. 符号写-写冲突（两条建议改写同一符号）；
 *  3. 符号写-读冲突（一条改写另一条依赖的符号，且二者之间没有前置排序）；
 *  4. 显式失效声明（invalidates）；
 *  5. 互斥组（mutexGroup）。
 */
import { ConflictEvidence, FixSuggestion, TextEdit } from './types';

function rangesOverlap(a: TextEdit, b: TextEdit): boolean {
  if (a.file !== b.file) return false;
  // 两个零长插入落在同一位置也视为冲突（插入顺序会改变结果）。
  if (a.start === a.end && b.start === b.end) return a.start === b.start;
  return a.start < b.end && b.start < a.end;
}

/** 计算每个建议的前置可达集（requires 的传递闭包，不含自身）。 */
export function prerequisiteReachability(suggestions: FixSuggestion[]): Map<string, Set<string>> {
  const byId = new Map(suggestions.map((s) => [s.id, s]));
  const reach = new Map<string, Set<string>>();
  for (const s of suggestions) reach.set(s.id, new Set());

  const visit = (start: string): Set<string> => {
    const cached = reach.get(start);
    if (cached && cached.size > 0) return cached;
    const out = cached ?? new Set<string>();
    const origin = byId.get(start);
    for (const req of origin?.requires ?? []) {
      if (out.has(req)) continue;
      out.add(req);
      if (byId.has(req)) {
        for (const t of visit(req)) out.add(t);
      }
    }
    reach.set(start, out);
    return out;
  };
  for (const s of suggestions) visit(s.id);
  return reach;
}

/**
 * 计算所有建议两两之间的冲突证据。
 * 返回 id -> 与其它建议的冲突列表（双向记录）。
 */
export function detectConflicts(suggestions: FixSuggestion[]): Map<string, ConflictEvidence[]> {
  const conflicts = new Map<string, ConflictEvidence[]>();
  const reach = prerequisiteReachability(suggestions);

  const add = (from: string, evidence: ConflictEvidence): void => {
    const list = conflicts.get(from) ?? [];
    list.push(evidence);
    conflicts.set(from, list);
  };
  const addPair = (a: string, b: string, kind: ConflictEvidence['kind'], detail: string): void => {
    add(a, { kind, otherId: b, detail });
    add(b, { kind, otherId: a, detail });
  };

  for (let i = 0; i < suggestions.length; i++) {
    for (let j = i + 1; j < suggestions.length; j++) {
      const a = suggestions[i];
      const b = suggestions[j];

      // 1. 文本重叠
      for (const ea of a.edits) {
        for (const eb of b.edits) {
          if (rangesOverlap(ea, eb)) {
            addPair(
              a.id,
              b.id,
              'text-overlap',
              `文件 ${ea.file} 的编辑区间 [${ea.start}, ${ea.end}) 与 [${eb.start}, ${eb.end}) 重叠`,
            );
          }
        }
      }

      // 2. 符号写-写
      const writesA = new Set(a.writesSymbols ?? []);
      const writesB = new Set(b.writesSymbols ?? []);
      for (const sym of writesA) {
        if (writesB.has(sym)) {
          addPair(a.id, b.id, 'symbol-write-write', `两条建议都改写符号 ${sym}`);
        }
      }

      // 3. 符号写-读：仅当二者之间没有 requires 排序时才算冲突
      const ordered = reach.get(a.id)?.has(b.id) || reach.get(b.id)?.has(a.id);
      if (!ordered) {
        const readsA = new Set(a.readsSymbols ?? []);
        const readsB = new Set(b.readsSymbols ?? []);
        for (const sym of writesA) {
          if (readsB.has(sym)) {
            addPair(a.id, b.id, 'symbol-write-read', `${a.id} 改写了 ${b.id} 依赖的符号 ${sym}`);
          }
        }
        for (const sym of writesB) {
          if (readsA.has(sym)) {
            addPair(a.id, b.id, 'symbol-write-read', `${b.id} 改写了 ${a.id} 依赖的符号 ${sym}`);
          }
        }
      }

      // 4. 显式失效声明
      if ((a.invalidates ?? []).includes(b.id)) {
        addPair(a.id, b.id, 'explicit-invalidation', `${a.id} 声明会使 ${b.id} 失效`);
      }
      if ((b.invalidates ?? []).includes(a.id)) {
        addPair(a.id, b.id, 'explicit-invalidation', `${b.id} 声明会使 ${a.id} 失效`);
      }

      // 5. 互斥组
      if (a.mutexGroup !== undefined && a.mutexGroup === b.mutexGroup) {
        addPair(a.id, b.id, 'mutex-group', `同属互斥组 ${a.mutexGroup}，最多选中一个`);
      }
    }
  }
  return conflicts;
}
