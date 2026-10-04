import type { Position, TextEdit, TextRange } from "./types.ts";

export function comparePositions(a: Position, b: Position): number {
  if (a.line !== b.line) return a.line - b.line;
  return a.character - b.character;
}

/** 计算每一行的起始偏移量 */
export function lineOffsets(content: string): number[] {
  const offsets = [0];
  for (let i = 0; i < content.length; i++) {
    if (content[i] === "\n") offsets.push(i + 1);
  }
  return offsets;
}

export function positionToOffset(
  content: string,
  offsets: number[],
  pos: Position,
): number {
  const line = offsets[pos.line];
  if (line === undefined) {
    throw new Error(`行号越界: line=${pos.line}（文件共 ${offsets.length} 行）`);
  }
  const lineEnd =
    pos.line + 1 < offsets.length ? offsets[pos.line + 1]! : content.length;
  const offset = line + pos.character;
  if (offset > lineEnd) {
    throw new Error(
      `列号越界: line=${pos.line} character=${pos.character}（该行长度 ${
        lineEnd - line
      }）`,
    );
  }
  return offset;
}

export function offsetToPosition(offsets: number[], offset: number): Position {
  let lo = 0;
  let hi = offsets.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (offsets[mid]! <= offset) lo = mid;
    else hi = mid - 1;
  }
  return { line: lo, character: offset - offsets[lo]! };
}

export interface OffsetRange {
  start: number;
  end: number;
}

export function editToOffsets(
  content: string,
  edit: TextEdit,
): OffsetRange {
  const offsets = lineOffsets(content);
  const start = positionToOffset(content, offsets, edit.range.start);
  const end = positionToOffset(content, offsets, edit.range.end);
  if (end < start) {
    throw new Error(
      `非法范围：end 在 start 之前（${formatRange(edit.range)}）`,
    );
  }
  return { start, end };
}

export function formatRange(range: TextRange): string {
  return `${range.start.line + 1}:${range.start.character + 1}-${
    range.end.line + 1
  }:${range.end.character + 1}`;
}

/**
 * 判断同一文件内两个编辑是否冲突。
 * 注意：不重叠只是必要条件之一，本函数处理的是文本层面的冲突；
 * 语义冲突（前置条件失效、conflictKeys）在别处判定。
 */
export function editsConflict(a: OffsetRange, b: OffsetRange): boolean {
  const aEmpty = a.start === a.end;
  const bEmpty = b.start === b.end;
  if (aEmpty && bEmpty) {
    // 两个插入在同一位置：应用顺序不确定，视为冲突
    return a.start === b.start;
  }
  if (aEmpty) return pointConflictsWithRange(a.start, b);
  if (bEmpty) return pointConflictsWithRange(b.start, a);
  // 半开区间相交
  return a.start < b.end && b.start < a.end;
}

function pointConflictsWithRange(p: number, r: OffsetRange): boolean {
  // 插入点落在被替换区间内部，或恰好等于起点（先后语义不明）→ 冲突；
  // 恰好在终点之后插入是确定性的，不冲突。
  return p >= r.start && p < r.end;
}
