import type { Assoc, Edit, MatchRecord, ReplaceResult, Replacement } from './types.js';
import { findAllMatches } from './match.js';
import { expandReplacement } from './replacement.js';

// 核心替换：先在旧文本上冻结全部非重叠匹配，再同时应用。
// 替换文本中产生的新匹配在本轮不会被处理。
export function replaceAll(text: string, pattern: RegExp | string, replacement: Replacement): ReplaceResult {
  const matches = findAllMatches(text, pattern);
  const edits: Edit[] = [];
  let out = '';
  let oldCursor = 0;
  for (const m of matches) {
    out += text.slice(oldCursor, m.start);
    const repText = renderReplacement(replacement, m, text);
    const newStart = out.length;
    out += repText;
    const edit: Edit = {
      oldStart: m.start,
      oldEnd: m.end,
      newStart,
      newEnd: out.length,
      oldText: m.text,
      newText: repText,
    };
    edits.push(edit);
    oldCursor = m.end;
  }
  out += text.slice(oldCursor);
  const newText = out;

  function mapPosition(pos: number, assoc: Assoc = 'start'): number {
    for (const e of edits) {
      if (pos < e.oldStart) {
        return pos + (e.newStart - e.oldStart);
      }
      if (pos > e.oldEnd) {
        continue;
      }
      if (e.oldStart === e.oldEnd) {
        if (assoc === 'end') {
          return e.newEnd;
        }
        return e.newStart;
      }
      if (pos === e.oldStart) {
        return e.newStart;
      }
      if (pos === e.oldEnd) {
        return e.newEnd;
      }
      if (assoc === 'end') {
        return e.newEnd;
      }
      return e.newStart;
    }
    const last = edits[edits.length - 1];
    let totalDelta = 0;
    if (last !== undefined) {
      totalDelta = last.newEnd - last.oldEnd;
    }
    return pos + totalDelta;
  }

  function mapRange(start: number, end: number): [number, number] {
    return [mapPosition(start, 'start'), mapPosition(end, 'end')];
  }

  return { oldText: text, newText, matches, edits, mapPosition, mapRange };
}

function renderReplacement(replacement: Replacement, m: MatchRecord, text: string): string {
  if (typeof replacement === 'function') {
    return replacement(m.text, m.groups, m.start, text);
  }
  return expandReplacement(replacement, m, text);
}

// 朴素拼接模型：按编辑把旧文本切片与替换文本依次拼接。
export function applyForward(oldText: string, edits: Edit[]): string {
  let out = '';
  let cursor = 0;
  for (const e of edits) {
    out += oldText.slice(cursor, e.oldStart) + e.newText;
    cursor = e.oldEnd;
  }
  out += oldText.slice(cursor);
  return out;
}

// 逆向应用：在新文本上把每个编辑的新区间换回旧文本，用于撤销与核对。
export function applyInverse(newText: string, edits: Edit[]): string {
  let out = '';
  let cursor = 0;
  for (const e of edits) {
    out += newText.slice(cursor, e.newStart) + e.oldText;
    cursor = e.newEnd;
  }
  out += newText.slice(cursor);
  return out;
}
