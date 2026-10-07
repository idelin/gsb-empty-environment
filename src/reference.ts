/**
 * Independent line-by-line reference implementation, used to cross-check
 * the offset-based main implementation in `indent.ts`.
 *
 * It splits the document into physical lines, transforms each touched
 * line in isolation, rejoins, and remaps selections through per-line
 * prefix deltas. It shares only the small visual-column helpers in
 * `visual.ts` (the spec's ground truth for tab stops).
 */

import type { Selection } from "./edits.ts";
import type { IndentOptions } from "./indent.ts";
import { TAB_WIDTH, prevTabStop, tabInsertion, visualColumn } from "./visual.ts";

export interface ReferenceResult {
  text: string;
  selections: Selection[];
}

interface LineInfo {
  content: string; // line text without the trailing "\n"
  start: number; // offset of the line in the original text
}

function splitLines(text: string): LineInfo[] {
  const lines: LineInfo[] = [];
  let start = 0;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) {
      lines.push({ content: text.slice(start, i), start });
      start = i + 1;
    }
  }
  lines.push({ content: text.slice(start), start });
  return lines;
}

function lineIndexAt(lines: readonly LineInfo[], offset: number): number {
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].start <= offset) return i;
  }
  return 0;
}

function transformLine(
  content: string,
  mode: "indent" | "unindent",
  tabWidth: number,
  useTabs: boolean,
): { content: string; removed: number; inserted: string } {
  if (mode === "indent") {
    if (content.length === 0) return { content, removed: 0, inserted: "" };
    const inserted = tabInsertion(0, useTabs, tabWidth);
    return { content: inserted + content, removed: 0, inserted };
  }
  let wsEnd = 0;
  while (wsEnd < content.length && (content[wsEnd] === " " || content[wsEnd] === "\t")) wsEnd++;
  if (wsEnd === 0) return { content, removed: 0, inserted: "" };
  const width = visualColumn(content, wsEnd, tabWidth);
  const target = prevTabStop(width, tabWidth);
  let removed = 0;
  let remaining = content.slice(0, wsEnd);
  while (remaining.length > 0 && visualColumn(remaining, remaining.length, tabWidth) > target) {
    remaining = remaining.slice(1);
    removed++;
  }
  const pad = " ".repeat(target - visualColumn(remaining, remaining.length, tabWidth));
  return { content: pad + content.slice(removed), removed, inserted: pad };
}

export function referenceApply(
  text: string,
  selections: readonly Selection[],
  mode: "indent" | "unindent",
  opts: IndentOptions = {},
): ReferenceResult {
  const tabWidth = opts.tabWidth ?? TAB_WIDTH;
  const useTabs = opts.useTabs ?? false;
  const lines = splitLines(text);

  // Determine touched lines independently from the split lines.
  const touched = new Set<number>();
  for (const sel of selections) {
    const s = Math.min(sel.anchor, sel.head);
    const e = Math.max(sel.anchor, sel.head);
    const first = lineIndexAt(lines, s);
    let last = lineIndexAt(lines, e);
    if (e > s && lines[last].start === e) last--;
    for (let l = first; l <= last; l++) touched.add(l);
  }

  const transformed = lines.map((line, idx) =>
    touched.has(idx)
      ? transformLine(line.content, mode, tabWidth, useTabs)
      : { content: line.content, removed: 0, inserted: "" },
  );

  // New offset of the start of each line.
  const newStarts = new Array<number>(lines.length);
  let acc = 0;
  for (let i = 0; i < lines.length; i++) {
    newStarts[i] = acc;
    acc += transformed[i].content.length + 1; // +1 for "\n"
  }

  const mapOffset = (pos: number): number => {
    const l = lineIndexAt(lines, pos);
    const col = pos - lines[l].start;
    const t = transformed[l];
    let newCol: number;
    if (col <= t.removed) {
      // Inside the replaced prefix: pure insertions carry the position
      // forward; removals clamp it to the line start.
      newCol = t.removed === 0 ? col + t.inserted.length : 0;
    } else {
      newCol = col - t.removed + t.inserted.length;
    }
    return newStarts[l] + newCol;
  };

  return {
    text: transformed.map((t) => t.content).join("\n"),
    selections: selections.map((s) => ({ anchor: mapOffset(s.anchor), head: mapOffset(s.head) })),
  };
}
