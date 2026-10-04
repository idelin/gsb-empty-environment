import type {
  ConflictEvidence,
  FileState,
  FixSuggestion,
  TextEdit,
} from "./types.ts";
import {
  editToOffsets,
  editsConflict,
  formatRange,
  type OffsetRange,
} from "./ranges.ts";
import { preconditionFile } from "./preconditions.ts";

interface ResolvedEdit {
  edit: TextEdit;
  range: OffsetRange;
}

function resolveEdits(
  files: FileState,
  suggestion: FixSuggestion,
): ResolvedEdit[] {
  const resolved: ResolvedEdit[] = [];
  for (const edit of suggestion.edits) {
    const content = files[edit.file];
    if (content === undefined) continue; // 非法编辑在校验阶段已记录
    try {
      resolved.push({ edit, range: editToOffsets(content, edit) });
    } catch {
      // 范围越界：校验阶段已记录，这里跳过
    }
  }
  return resolved;
}

/**
 * 分析两个建议之间的硬冲突（不可共存）：
 * 1. 文本编辑重叠（含插入点落在对方替换区间内等边界情形）；
 * 2. 共享 conflictKeys 语义标签。
 *
 * 注意：文本范围不重叠不代表兼容 —— 一个建议的编辑可能使另一个建议的
 * 前置条件失效（读写干涉），这类问题通过应用后重新检查前置条件来捕获，
 * 见 select.ts 的模拟应用阶段。
 */
export function analyzePair(
  a: FixSuggestion,
  b: FixSuggestion,
  files: FileState,
): ConflictEvidence[] {
  const evidence: ConflictEvidence[] = [];

  const aKeys = new Set(a.conflictKeys ?? []);
  for (const key of b.conflictKeys ?? []) {
    if (aKeys.has(key)) {
      evidence.push({
        kind: "conflict-key",
        otherId: b.id,
        detail: `双方声明了相同的语义冲突标签 ${JSON.stringify(key)}`,
      });
    }
  }

  const aEdits = resolveEdits(files, a);
  const bEdits = resolveEdits(files, b);
  for (const x of aEdits) {
    for (const y of bEdits) {
      if (x.edit.file !== y.edit.file) continue;
      if (editsConflict(x.range, y.range)) {
        evidence.push({
          kind: "edit-overlap",
          otherId: b.id,
          detail:
            `文件 ${x.edit.file} 中编辑范围重叠：` +
            `${a.id} 的 ${formatRange(x.edit.range)} 与 ` +
            `${b.id} 的 ${formatRange(y.edit.range)}`,
        });
      }
    }
  }
  return evidence;
}

/**
 * 读写干涉：a 的编辑改动的文件，是否是 b 的前置条件所读取的文件。
 * 不是硬冲突，但应用顺序会影响 b 的前置条件是否仍成立，
 * 用于生成“前置条件失效”的证据归因。
 */
export function writesInterfereWithReads(
  writer: FixSuggestion,
  reader: FixSuggestion,
): string[] {
  const written = new Set(writer.edits.map((e) => e.file));
  const interfered: string[] = [];
  for (const pc of reader.preconditions) {
    const file = preconditionFile(pc);
    if (written.has(file)) interfered.push(file);
  }
  return interfered;
}
