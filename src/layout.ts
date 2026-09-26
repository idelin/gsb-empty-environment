import type {
  Atom, Conflict, Degradation, Direction, KeyFragment, LayoutLine, LayoutResult, VisualItem,
} from './types.ts';
import { costOf, exhaustiveKeepSet, greedyKeepSet } from './optimal.ts';

export type EllipsisMode = 'start' | 'middle' | 'end';

export interface LayoutParams {
  lineWidth: number;
  maxLines: number;
  lineHeight: number;
  ellipsis?: string;
  mode?: EllipsisMode;
  keyFragments?: KeyFragment[];
  /** 剩余原子数不超过该值时末行用穷举求最优保留,否则贪心 */
  exhaustiveThreshold?: number;
}

interface PendingItem {
  kind: 'atom' | 'ellipsis';
  atom: Atom | null;
  /** 省略号覆盖的原子区间(逻辑序,[start,end)) */
  covers: { atomStart: number; atomEnd: number } | null;
  width: number;
  dir: Direction;
}

/** 贪心折行:原子逐个填入,超宽则换行;实体/簇永不拆开。 */
function flowLines(widths: number[], lineWidth: number): number[][] {
  const lines: number[][] = [];
  let current: number[] = [];
  let w = 0;
  for (let i = 0; i < widths.length; i++) {
    if (current.length > 0 && w + widths[i] > lineWidth) {
      lines.push(current);
      current = [];
      w = 0;
    }
    current.push(i);
    w += widths[i];
  }
  if (current.length > 0) lines.push(current);
  return lines;
}

/** 简化双向重排:保持 run 顺序,RTL run 内部反转(LTR 基向段落)。 */
function reorderBidi(items: PendingItem[]): PendingItem[] {
  const runs: PendingItem[][] = [];
  for (const it of items) {
    const last = runs[runs.length - 1];
    if (last && last[0].dir === it.dir) last.push(it);
    else runs.push([it]);
  }
  const out: PendingItem[] = [];
  for (const run of runs) {
    if (run[0].dir === 'rtl') {
      for (let i = run.length - 1; i >= 0; i--) out.push(run[i]);
    } else {
      out.push(...run);
    }
  }
  return out;
}

function buildLine(items: PendingItem[], y: number): LayoutLine {
  const visual = reorderBidi(items);
  let x = 0;
  const out: VisualItem[] = [];
  for (const it of visual) {
    if (it.kind === 'atom') {
      out.push({ kind: 'atom', atom: it.atom!, x, width: it.width });
    } else {
      out.push({ kind: 'ellipsis', x, width: it.width, covers: it.covers! });
    }
    x += it.width;
  }
  return { y, items: out, width: x };
}

function atomItem(atom: Atom, width: number): PendingItem {
  return { kind: 'atom', atom, covers: null, width, dir: atom.dir };
}

function ellipsisItem(width: number, atomStart: number, atomEnd: number, dir: Direction): PendingItem {
  return { kind: 'ellipsis', atom: null, covers: { atomStart, atomEnd }, width, dir };
}

export function layoutAtoms(
  atoms: Atom[],
  widths: number[],
  params: LayoutParams,
  ellipsisWidth: number,
  metricsVersion: number,
  generation: number,
): LayoutResult {
  const mode = params.mode ?? 'end';
  const keyFragments = params.keyFragments ?? [];
  const threshold = params.exhaustiveThreshold ?? 20;
  const conflicts: Conflict[] = [];
  const degradations: Degradation[] = [];
  const lines: LayoutLine[] = [];

  const flowed = flowLines(widths, params.lineWidth);
  const truncated = flowed.length > params.maxLines;
  const visibleLines = truncated ? flowed.slice(0, params.maxLines - 1) : flowed;

  for (let li = 0; li < visibleLines.length; li++) {
    const items = visibleLines[li].map((ai) => atomItem(atoms[ai], widths[ai]));
    lines.push(buildLine(items, li * params.lineHeight));
  }

  if (truncated) {
    const startAtom = flowed[params.maxLines - 1][0];
    const rest: number[] = [];
    for (let i = startAtom; i < atoms.length; i++) rest.push(i);
    const restWidths = rest.map((i) => widths[i]);
    const y = (params.maxLines - 1) * params.lineHeight;

    // 退化:省略号本身超宽,放不下任何省略号
    if (ellipsisWidth > params.lineWidth) {
      degradations.push({ type: 'ellipsis-too-wide', ellipsisWidth, lineWidth: params.lineWidth });
      const items: PendingItem[] = [];
      let w = 0;
      for (let k = 0; k < rest.length; k++) {
        if (w + restWidths[k] > params.lineWidth && items.length > 0) break;
        items.push(atomItem(atoms[rest[k]], restWidths[k]));
        w += restWidths[k];
      }
      lines.push(buildLine(items, y));
    } else if (mode === 'end') {
      const budget = params.lineWidth - ellipsisWidth;
      const items: PendingItem[] = [];
      let w = 0;
      let kept = 0;
      for (let k = 0; k < rest.length; k++) {
        if (w + restWidths[k] > budget) break;
        items.push(atomItem(atoms[rest[k]], restWidths[k]));
        w += restWidths[k];
        kept++;
      }
      const dir = kept > 0 ? atoms[rest[kept - 1]].dir : 'ltr';
      items.push(ellipsisItem(ellipsisWidth, rest[kept], atoms.length, dir));
      lines.push(buildLine(items, y));
    } else if (mode === 'start') {
      const budget = params.lineWidth - ellipsisWidth;
      let w = 0;
      let kept = 0; // 从尾部向前保留的个数
      for (let k = rest.length - 1; k >= 0; k--) {
        if (w + restWidths[k] > budget) break;
        w += restWidths[k];
        kept++;
      }
      const firstKept = rest.length - kept;
      const items: PendingItem[] = [
        ellipsisItem(ellipsisWidth, rest[0], rest[firstKept], atoms[rest[rest.length - 1]].dir),
      ];
      for (let k = firstKept; k < rest.length; k++) {
        items.push(atomItem(atoms[rest[k]], restWidths[k]));
      }
      lines.push(buildLine(items, y));
    } else {
      // middle:关键片段必须保留,其余按最优保留目标填充
      const keyAtoms = rest.map((ai) =>
        keyFragments.some((f) => atoms[ai].start < f.end && atoms[ai].end > f.start));
      const input = { widths: restWidths, keyAtoms, lineWidth: params.lineWidth, ellipsisWidth };
      const requiredCost = costOf(keyAtoms, restWidths, ellipsisWidth);
      if (requiredCost > params.lineWidth) {
        conflicts.push({
          type: 'key-fragment-over-capacity',
          fragments: keyFragments.map((f) => ({
            id: f.id,
            requiredWidth: rest.reduce((sum, ai, k) =>
              sum + (atoms[ai].start < f.end && atoms[ai].end > f.start ? restWidths[k] : 0), 0),
          })),
          availableWidth: params.lineWidth,
          requiredWidth: requiredCost,
        });
      }
      const solved = rest.length <= threshold ? exhaustiveKeepSet(input) : greedyKeepSet(input);
      const items: PendingItem[] = [];
      let k = 0;
      while (k < rest.length) {
        if (solved.kept[k]) {
          items.push(atomItem(atoms[rest[k]], restWidths[k]));
          k++;
        } else {
          let gapEnd = k;
          while (gapEnd < rest.length && !solved.kept[gapEnd]) gapEnd++;
          const dir = k > 0 ? atoms[rest[k - 1]].dir : 'ltr';
          items.push(ellipsisItem(ellipsisWidth, rest[k], rest[gapEnd - 1] + 1, dir));
          k = gapEnd;
        }
      }
      lines.push(buildLine(items, y));
    }
  }

  return { lines, truncated, conflicts, degradations, metricsVersion, generation };
}
