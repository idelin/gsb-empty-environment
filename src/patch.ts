/**
 * 版本补丁：一组「顺序应用」的编辑。
 *
 * 每个编辑的坐标都基于它之前所有编辑应用完成后的文档状态，
 * 因此同一个补丁内部、以及连续多个补丁之间，都允许重叠替换
 * （后一个编辑可以改到前一个编辑刚插入的文本）。
 */
export interface Edit {
  /** 替换区间起点（含），基于应用本编辑时的文档 */
  start: number;
  /** 替换区间终点（不含），基于应用本编辑时的文档 */
  end: number;
  /** 替换后的文本；start === end 时为纯插入，text 为空时为纯删除 */
  text: string;
}

export interface Patch {
  edits: Edit[];
}

export class PatchError extends Error {}

export function validateEdit(doc: string, edit: Edit): void {
  if (!Number.isInteger(edit.start) || !Number.isInteger(edit.end)) {
    throw new PatchError(`edit offsets must be integers, got [${edit.start}, ${edit.end})`);
  }
  if (edit.start < 0 || edit.end < edit.start || edit.end > doc.length) {
    throw new PatchError(
      `edit [${edit.start}, ${edit.end}) out of bounds for document of length ${doc.length}`
    );
  }
}

export function applyEdit(doc: string, edit: Edit): string {
  validateEdit(doc, edit);
  return doc.slice(0, edit.start) + edit.text + doc.slice(edit.end);
}

export function applyPatch(doc: string, patch: Patch): string {
  let out = doc;
  for (const edit of patch.edits) {
    out = applyEdit(out, edit);
  }
  return out;
}

/**
 * 组合补丁：把多个连续补丁合并为一个。
 * 由于编辑本来就是按顺序解释的，组合就是编辑序列的连接；
 * 这保证了「组合映射 ≡ 逐步映射」。
 */
export function composePatches(...patches: Patch[]): Patch {
  const edits: Edit[] = [];
  for (const patch of patches) {
    for (const edit of patch.edits) {
      edits.push(edit);
    }
  }
  return { edits };
}

export type MappedPosition =
  | { kind: 'mapped'; position: number }
  | { kind: 'tombstone' };

/**
 * 单个编辑下的位置映射。规则（编辑区间为 [start, end)，替换为 text）：
 * - position < start：不变；
 * - position > end：平移 delta；
 * - position === start 且为纯插入（start === end）：锚点保持在插入文本之前（左亲和）；
 * - position === start 且为删除/替换：锚点幸存，停留在区间起点；
 * - position === end（end > start）：锚点落在替换文本之后；
 * - start < position < end：位置被删除覆盖，返回 tombstone。
 */
export function mapPositionThroughEdit(position: number, edit: Edit): MappedPosition {
  const { start, end, text } = edit;
  if (position < start) {
    return { kind: 'mapped', position };
  }
  if (position > end) {
    return { kind: 'mapped', position: position - (end - start) + text.length };
  }
  if (position === start) {
    return { kind: 'mapped', position: start };
  }
  if (position === end) {
    return { kind: 'mapped', position: start + text.length };
  }
  return { kind: 'tombstone' };
}

/** 依次穿过整个补丁的位置映射；一旦进入墓碑态便保持。 */
export function mapPosition(position: number, patch: Patch): MappedPosition {
  let current: MappedPosition = { kind: 'mapped', position };
  for (const edit of patch.edits) {
    if (current.kind === 'tombstone') {
      break;
    }
    current = mapPositionThroughEdit(current.position, edit);
  }
  return current;
}
