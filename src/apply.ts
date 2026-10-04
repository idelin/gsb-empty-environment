import type { FileState, TextEdit } from "./types.ts";
import { editToOffsets } from "./ranges.ts";

/**
 * 把一组编辑应用到文件状态上，返回新状态（不修改入参）。
 * 同一文件内的编辑按起点偏移降序应用，保证偏移互不干扰。
 * 调用前必须保证编辑两两不冲突（由组合器负责）。
 */
export function applyEdits(files: FileState, edits: TextEdit[]): FileState {
  const byFile = new Map<string, TextEdit[]>();
  for (const edit of edits) {
    const list = byFile.get(edit.file);
    if (list) list.push(edit);
    else byFile.set(edit.file, [edit]);
  }
  const result: FileState = { ...files };
  for (const [file, fileEdits] of byFile) {
    const content = result[file];
    if (content === undefined) {
      throw new Error(`编辑指向不存在的文件: ${file}`);
    }
    const resolved = fileEdits.map((edit) => ({
      edit,
      range: editToOffsets(content, edit),
    }));
    resolved.sort((x, y) => y.range.start - x.range.start);
    let next = content;
    for (const { edit, range } of resolved) {
      next = next.slice(0, range.start) + edit.newText + next.slice(range.end);
    }
    result[file] = next;
  }
  return result;
}
