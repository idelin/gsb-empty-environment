// Visual <-> source mapping: hit testing, selection (direction
// preserved), copy text and the full accessible name.

import type { LayoutResult, Rect, VisualItem } from './types.ts';

// Map a visual position to a source offset. Returns null when the
// position falls on the ellipsis (no source content there).
export function hitTest(result: LayoutResult, line: number, x: number): number | null {
  const row = result.lines[line];
  if (!row || row.length === 0) return null;
  for (const it of row) {
    if (x >= it.x && x < it.x + it.width) {
      if (it.kind === 'ellipsis') return null;
      const rel = it.width === 0 ? 0 : (x - it.x) / it.width;
      // In an rtl cluster the left visual edge is the source end.
      if (it.dir === 'ltr') return rel < 0.5 ? it.srcStart : it.srcEnd;
      return rel < 0.5 ? it.srcEnd : it.srcStart;
    }
  }
  // Beyond the last item: snap to the nearest edge.
  const last = row[row.length - 1];
  const first = row[0];
  if (x < first.x) return edgeOffset(first, 'near');
  if (x >= last.x + last.width) return edgeOffset(last, 'far');
  return edgeOffset(last, 'far');
}

function edgeOffset(it: VisualItem, side: 'near' | 'far'): number | null {
  if (it.kind === 'ellipsis') return null;
  if (it.dir === 'ltr') return side === 'near' ? it.srcStart : it.srcEnd;
  return side === 'near' ? it.srcEnd : it.srcStart;
}

export interface SelectionResult {
  anchor: number;
  focus: number;
  backward: boolean;             // selection direction in source order
  ranges: Array<{ start: number; end: number }>; // visible source ranges
  rects: Rect[];                 // visual highlight rectangles
  copiedText: string;            // full original text across the selection
}

export function selectRange(result: LayoutResult, anchor: number, focus: number): SelectionResult {
  const lo = Math.min(anchor, focus);
  const hi = Math.max(anchor, focus);
  const backward = focus < anchor;

  const selected: VisualItem[] = [];
  for (const row of result.lines) {
    for (const it of row) {
      if (it.kind !== 'cluster') continue;
      if (it.srcStart < hi && it.srcEnd > lo) selected.push(it);
    }
  }

  const bySource = selected.slice().sort((p, q) => p.srcStart - q.srcStart);
  const ranges: Array<{ start: number; end: number }> = [];
  for (const it of bySource) {
    const start = Math.max(it.srcStart, lo);
    const end = Math.min(it.srcEnd, hi);
    const last = ranges[ranges.length - 1];
    if (last && start <= last.end) last.end = Math.max(last.end, end);
    else ranges.push({ start, end });
  }

  const byLine = new Map<number, VisualItem[]>();
  for (const it of selected) {
    const arr = byLine.get(it.line) ?? [];
    arr.push(it);
    byLine.set(it.line, arr);
  }
  const rects: Rect[] = [];
  for (const [line, items] of [...byLine.entries()].sort((a, b) => a[0] - b[0])) {
    const x0 = Math.min(...items.map((i) => i.x));
    const x1 = Math.max(...items.map((i) => i.x + i.width));
    rects.push({ line, x: x0, width: x1 - x0 });
  }

  return {
    anchor,
    focus,
    backward,
    ranges,
    rects,
    copiedText: result.source.slice(lo, hi),
  };
}

// The accessible name is always the complete original text, regardless
// of truncation.
export function accessibleName(result: LayoutResult): string {
  return result.source;
}
