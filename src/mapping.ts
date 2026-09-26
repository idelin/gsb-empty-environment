import type { Atom, LayoutLine, LayoutResult } from './types.ts';

/** 命中测试:视觉坐标 -> 原文(逻辑)偏移。 */
export function caretFromPoint(result: LayoutResult, atoms: Atom[], x: number, y: number,
  lineHeight: number): number {
  const li = Math.max(0, Math.min(result.lines.length - 1, Math.round(y / lineHeight)));
  const line = result.lines[li];
  if (!line || line.items.length === 0) return 0;
  for (const item of line.items) {
    if (x < item.x + item.width) {
      const leftHalf = x < item.x + item.width / 2;
      if (item.kind === 'ellipsis') {
        return leftHalf
          ? atoms[item.covers.atomStart].start
          : atoms[item.covers.atomEnd - 1].end;
      }
      const a = item.atom;
      // RTL 原子:左边缘对应逻辑 end,右边缘对应逻辑 start
      if (a.dir === 'rtl') return leftHalf ? a.end : a.start;
      return leftHalf ? a.start : a.end;
    }
  }
  const last = line.items[line.items.length - 1];
  if (last.kind === 'ellipsis') return atoms[last.covers.atomEnd - 1].end;
  return last.atom.dir === 'rtl' ? last.atom.start : last.atom.end;
}

export interface SelectionRect {
  line: number;
  x: number;
  y: number;
  width: number;
}

export interface Selection {
  /** 逻辑锚点/焦点,方向原样保留(反向选择 anchor > focus) */
  anchor: number;
  focus: number;
  reversed: boolean;
  rects: SelectionRect[];
}

/** 逻辑选区 -> 各行视觉矩形(用于高亮);选择方向保留在 anchor/focus 中。 */
export function selectionRects(result: LayoutResult, anchor: number, focus: number): Selection {
  const lo = Math.min(anchor, focus);
  const hi = Math.max(anchor, focus);
  const rects: SelectionRect[] = [];
  result.lines.forEach((line: LayoutLine, li: number) => {
    for (const item of line.items) {
      if (item.kind !== 'atom') continue;
      const a = item.atom;
      if (a.start < hi && a.end > lo) {
        rects.push({ line: li, x: item.x, y: line.y, width: item.width });
      }
    }
  });
  return { anchor, focus, reversed: anchor > focus, rects };
}

/** 复制选区:始终得到逻辑序原文片段,与视觉方向无关。 */
export function copyText(accessibleName: string, anchor: number, focus: number): string {
  const lo = Math.min(anchor, focus);
  const hi = Math.max(anchor, focus);
  return accessibleName.slice(lo, hi);
}

export interface LinkFragment {
  linkId: string;
  /** 同一原范围:被省略区切开的片段仍指向链接的完整原文区间 */
  linkRange: { start: number; end: number };
  /** 可见片段的原文区间 */
  start: number;
  end: number;
  line: number;
  x: number;
  width: number;
}

/** 收集链接的可见片段;跨省略区的链接被分成多个片段但共享同一 linkRange。 */
export function linkFragments(result: LayoutResult, linkId: string): LinkFragment[] {
  const out: LinkFragment[] = [];
  result.lines.forEach((line: LayoutLine, li: number) => {
    let cur: LinkFragment | null = null;
    for (const item of line.items) {
      if (item.kind === 'atom' && item.atom.linkId === linkId) {
        const a = item.atom;
        if (cur && cur.end === a.start) {
          cur.end = a.end;
          cur.width += item.width;
        } else {
          cur = {
            linkId,
            linkRange: { ...(a.linkRange ?? { start: a.start, end: a.end }) },
            start: a.start, end: a.end, line: li, x: item.x, width: item.width,
          };
          out.push(cur);
        }
      } else {
        cur = null;
      }
    }
  });
  return out;
}
