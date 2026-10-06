/**
 * 标记语言测试辅助：
 *   raw 字符串中用 "|" 表示光标、"[...]" 表示选区（raw 坐标系）。
 * runEdit 模拟宿主的完整链路：
 *   raw 选区 --(buildPositionMap)--> display 选区 --(applyEdit)--> 结果
 *   结果光标 --(displayIndexToRawIndex)--> raw 光标，重新打上标记。
 * 断言直接比较带标记的字符串，例如 "12|34" -> 输入 "5" -> "125|34"。
 */

import { applyEdit, deleteBackward, deleteForward } from "../src/edit.ts";
import type { EditResult } from "../src/edit.ts";
import { buildPositionMap, displayIndexToRawIndex } from "../src/mapping.ts";

export interface ParsedSelection {
  raw: string;
  selStart: number;
  selEnd: number;
}

export function parseMarked(marked: string): ParsedSelection {
  let raw = "";
  let caret = -1;
  let open = -1;
  let close = -1;
  for (const ch of marked) {
    if (ch === "|") caret = raw.length;
    else if (ch === "[") open = raw.length;
    else if (ch === "]") close = raw.length;
    else raw += ch;
  }
  if (caret >= 0 && open < 0) return { raw, selStart: caret, selEnd: caret };
  if (open >= 0 && close >= 0 && caret < 0) return { raw, selStart: open, selEnd: close };
  throw new Error(`非法标记串: ${marked}`);
}

export function markAt(text: string, caret: number): string {
  return text.slice(0, caret) + "|" + text.slice(caret);
}

export interface MarkedResult {
  /** 结果 raw，带光标标记 */
  raw: string;
  /** 结果 display，带光标标记 */
  display: string;
}

function render(result: EditResult): MarkedResult {
  const rawCaret = displayIndexToRawIndex(result.raw, result.caret);
  return { raw: markAt(result.raw, rawCaret), display: markAt(result.display, result.caret) };
}

/** 在标记串描述的选区上插入文本（inserted 为 "" 即删除选区）。 */
export function runEdit(before: string, inserted: string): MarkedResult {
  const { raw, selStart, selEnd } = parseMarked(before);
  const map = buildPositionMap(raw);
  return render(
    applyEdit({
      raw,
      selectionStart: map.rawToDisplay[selStart],
      selectionEnd: map.rawToDisplay[selEnd],
      inserted,
    }),
  );
}

export function runBackspace(before: string): MarkedResult {
  const { raw, selStart } = parseMarked(before);
  const map = buildPositionMap(raw);
  return render(deleteBackward(raw, map.rawToDisplay[selStart]));
}

export function runForwardDelete(before: string): MarkedResult {
  const { raw, selStart } = parseMarked(before);
  const map = buildPositionMap(raw);
  return render(deleteForward(raw, map.rawToDisplay[selStart]));
}
