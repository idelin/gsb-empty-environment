/**
 * Main indent/unindent implementation.
 *
 * Works directly on document offsets. Lines touched by more than one
 * selection are deduplicated so each line is indented at most once.
 *
 * Semantics:
 * - A selection touches lines from its start line through its end line;
 *   when the selection ends exactly at the start of a line (and is not
 *   empty), that final line is NOT touched.
 * - Indent inserts whitespace at the start of every touched non-empty
 *   line, advancing column 0 to the next tab stop (one full tab stop).
 * - Unindent removes at most one tab stop of leading whitespace per
 *   touched line: existing tabs count by visual column, and the line is
 *   left exactly on the previous tab stop (padding with spaces if a
 *   removed tab overshoots).
 */

import type { Edit, Selection } from "./edits.ts";
import { TAB_WIDTH, prevTabStop, tabInsertion, visualColumn } from "./visual.ts";

export interface IndentOptions {
  tabWidth?: number;
  useTabs?: boolean;
}

export function computeLineStarts(text: string): number[] {
  const starts = [0];
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) starts.push(i + 1);
  }
  return starts;
}

/** Index of the line containing `offset`. */
export function lineOfOffset(starts: readonly number[], offset: number): number {
  let lo = 0;
  let hi = starts.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (starts[mid] <= offset) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}

function lineEnd(starts: readonly number[], text: string, line: number): number {
  const next = line + 1 < starts.length ? starts[line + 1] - 1 : text.length;
  return next; // index of "\n" or end of text
}

/** Sorted, deduplicated indices of lines touched by any selection. */
export function touchedLines(text: string, selections: readonly Selection[]): number[] {
  const starts = computeLineStarts(text);
  const set = new Set<number>();
  for (const sel of selections) {
    const s = Math.min(sel.anchor, sel.head);
    const e = Math.max(sel.anchor, sel.head);
    const first = lineOfOffset(starts, s);
    let last = lineOfOffset(starts, e);
    if (e > s && e === starts[last]) last--;
    for (let l = first; l <= last; l++) set.add(l);
  }
  return [...set].sort((a, b) => a - b);
}

export function computeIndentEdits(
  text: string,
  selections: readonly Selection[],
  opts: IndentOptions = {},
): Edit[] {
  const tabWidth = opts.tabWidth ?? TAB_WIDTH;
  const useTabs = opts.useTabs ?? false;
  const starts = computeLineStarts(text);
  const edits: Edit[] = [];
  for (const line of touchedLines(text, selections)) {
    const start = starts[line];
    if (lineEnd(starts, text, line) === start) continue; // skip empty lines
    // Insertion point is the line start (visual column 0).
    const inserted = tabInsertion(0, useTabs, tabWidth);
    edits.push({ offset: start, removedLen: 0, inserted });
  }
  return edits;
}

export function computeUnindentEdits(
  text: string,
  selections: readonly Selection[],
  opts: IndentOptions = {},
): Edit[] {
  const tabWidth = opts.tabWidth ?? TAB_WIDTH;
  const starts = computeLineStarts(text);
  const edits: Edit[] = [];
  for (const line of touchedLines(text, selections)) {
    const start = starts[line];
    const end = lineEnd(starts, text, line);
    let i = start;
    while (i < end && (text.charCodeAt(i) === 32 || text.charCodeAt(i) === 9)) i++;
    const wsLen = i - start;
    if (wsLen === 0) continue;
    const ws = text.slice(start, i);
    const width = visualColumn(ws, ws.length, tabWidth);
    const target = prevTabStop(width, tabWidth);
    // Visual width of each suffix ws[k..]; removing from the front
    // re-aligns tabs, so widths must be recomputed per suffix.
    const suffixWidth = new Array<number>(wsLen + 1);
    for (let k = wsLen; k >= 0; k--) {
      suffixWidth[k] = k === wsLen ? 0 : visualColumn(ws.slice(k), wsLen - k, tabWidth);
    }
    let removed = 0;
    while (removed < wsLen && suffixWidth[removed] > target) removed++;
    const pad = target - suffixWidth[removed]; // >= 0
    edits.push({ offset: start, removedLen: removed, inserted: " ".repeat(pad) });
  }
  return edits;
}
