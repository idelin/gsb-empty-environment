/**
 * 补全应用层：语言服务器基于旧文档版本返回补全项，本模块负责用版本编辑链
 * 核对旧响应、把替换范围映射到当前版本，并把补全作为一次可撤销事务应用。
 */

import type { VersionedDocument } from './document.ts';
import { RangeAffinity, type Range } from './range.ts';

/** 一条补全响应。replaceRange 与 version 都基于响应产生时的文档版本。 */
export interface CompletionItem {
  readonly label: string;
  readonly insertText: string;
  /** 主光标处的替换范围，坐标基于 version 指定的文档版本。 */
  readonly replaceRange: Range;
  /** 响应产生时的文档版本。 */
  readonly version: number;
}

export type ApplyCompletionFailure =
  /** 响应版本比文档还新，无法核对。 */
  | 'future-version'
  /** 后续编辑触及了某个替换范围的内部，映射失效。 */
  | 'invalidated'
  /** 映射后的多个光标范围互相重叠，无法安全应用。 */
  | 'overlapping-ranges';

export type ResolveCompletionResult =
  | { readonly ok: true; readonly ranges: Range[] }
  | { readonly ok: false; readonly reason: ApplyCompletionFailure };

export type ApplyCompletionResult =
  | { readonly ok: true; readonly version: number; readonly appliedRanges: Range[] }
  | { readonly ok: false; readonly reason: ApplyCompletionFailure };

/**
 * 核对补全响应并解析出当前版本下的替换范围，但不修改文档。
 *
 * @param secondaryRanges 多光标场景下次要光标的替换范围，坐标同样基于
 *   item.version。每个范围（含主范围）都会用相同的 insertText 替换。
 */
export function resolveCompletionRanges(
  doc: VersionedDocument,
  item: CompletionItem,
  secondaryRanges: readonly Range[] = [],
  affinity: RangeAffinity = RangeAffinity.Expand,
): ResolveCompletionResult {
  if (!Number.isInteger(item.version) || item.version < 0) {
    throw new RangeError(`非法的补全版本号: ${item.version}`);
  }
  if (item.version > doc.version) {
    return { ok: false, reason: 'future-version' };
  }
  const mapped: Range[] = [];
  for (const stale of [item.replaceRange, ...secondaryRanges]) {
    const current = doc.mapRange(stale, item.version, affinity);
    if (current === undefined) return { ok: false, reason: 'invalidated' };
    mapped.push(current);
  }
  if (hasOverlap(mapped)) return { ok: false, reason: 'overlapping-ranges' };
  return { ok: true, ranges: mapped };
}

/**
 * 应用补全：核对旧响应 → 映射全部光标范围 → 作为一次可撤销事务提交。
 * 成功时返回新版本号与实际替换的范围（当前版本坐标）；失败时文档不变。
 */
export function applyCompletion(
  doc: VersionedDocument,
  item: CompletionItem,
  secondaryRanges: readonly Range[] = [],
  affinity: RangeAffinity = RangeAffinity.Expand,
): ApplyCompletionResult {
  const resolved = resolveCompletionRanges(doc, item, secondaryRanges, affinity);
  if (!resolved.ok) return resolved;
  const edits = resolved.ranges.map((target) => ({
    start: target.start,
    end: target.end,
    text: item.insertText,
  }));
  const version = doc.applyTransaction(edits);
  return { ok: true, version, appliedRanges: resolved.ranges };
}

/** 检查映射后的范围是否互相重叠（两个重合的空范围也算冲突）。 */
function hasOverlap(ranges: readonly Range[]): boolean {
  const sorted = [...ranges].sort((a, b) => a.start - b.start || a.end - b.end);
  for (let i = 0; i + 1 < sorted.length; i++) {
    const a = sorted[i];
    const b = sorted[i + 1];
    if (a.end > b.start) return true;
    if (a.end === b.start && a.start === a.end && b.start === b.end) return true;
  }
  return false;
}
