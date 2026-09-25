// Core layout: greedy line wrapping over grapheme clusters, truncation
// (end / middle / key-fragment retention), bidi visual ordering, link
// fragment collection. All measurements come from the fixture metric
// table; no browser layout engine is used.

import type {
  ClusterItem, ConflictInfo, Dir, LayoutOptions, LayoutResult,
  LinkFragment, MetricTable, Rect, VisualItem,
} from './types.ts';
import { levelOf, reorderVisual } from './bidi.ts';
import { ellipsisWidth, makeEllipsisItem, measure } from './metrics.ts';
import { exhaustiveBest } from './optimize.ts';

interface WrapNode {
  it: ClusterItem;
  canBreak: boolean;   // a line break is allowed after this node
  atomicOpen: boolean; // the atomic group continues past this node
}

function toWrapNodes(items: ClusterItem[]): WrapNode[] {
  return items.map((it, i) => {
    const next = items[i + 1];
    const groupContinues = it.atomicId !== 0 && next !== undefined && next.atomicId === it.atomicId;
    const groupStartsNext = next !== undefined && next.atomicId !== 0 && next.atomicId !== it.atomicId;
    return {
      it,
      canBreak: (it.breakAfter && !groupContinues) || groupStartsNext,
      atomicOpen: groupContinues,
    };
  });
}

function isSpace(it: ClusterItem): boolean {
  return it.kind === 'cluster' && /^\s+$/.test(it.text);
}

function trimEnd(line: ClusterItem[]): void {
  while (line.length > 0 && isSpace(line[line.length - 1])) line.pop();
}

function trimStart(line: ClusterItem[]): void {
  while (line.length > 0 && isSpace(line[0])) line.shift();
}

// Greedy wrapping. Atomic (unbreakable) groups are never split; they
// overflow their line instead. Non-atomic over-wide content hard-breaks
// at cluster boundaries. Trailing spaces are dropped from each line.
export function wrapLines(items: ClusterItem[], lineWidth: number): ClusterItem[][] {
  const trimmedInput = items.slice();
  trimStart(trimmedInput); // leading spaces of a block never render
  const nodes = toWrapNodes(trimmedInput);
  const lines: ClusterItem[][] = [];
  let cur: WrapNode[] = [];
  let curWidth = 0;
  let lastBreak = -1; // index into cur; break allowed after cur[lastBreak]

  const recompute = (): void => {
    curWidth = 0;
    lastBreak = -1;
    for (let i = 0; i < cur.length; i++) {
      curWidth += cur[i].it.width;
      if (cur[i].canBreak) lastBreak = i;
    }
  };

  const flush = (keep: number): void => {
    const head = cur.slice(0, keep).map((n) => n.it);
    trimEnd(head);
    trimStart(head); // a break swallows the space it broke at
    if (head.length > 0) lines.push(head);
    cur = cur.slice(keep);
    while (cur.length > 0 && isSpace(cur[0].it)) cur.shift();
    recompute();
  };

  for (const node of nodes) {
    if (cur.length > 0 && curWidth + node.it.width > lineWidth) {
      if (lastBreak >= 0) {
        flush(lastBreak + 1);
        if (cur.length > 0 && curWidth + node.it.width > lineWidth && !cur[cur.length - 1].atomicOpen) {
          flush(cur.length); // remaining fragment still too wide: hard break
        }
      } else if (!cur[cur.length - 1].atomicOpen) {
        flush(cur.length); // no break opportunity: hard break at cluster edge
      }
      // else: inside an unbreakable group -> keep accumulating (overflow)
    }
    cur.push(node);
    curWidth += node.it.width;
    if (node.canBreak) lastBreak = cur.length - 1;
  }
  const rest = cur.map((n) => n.it);
  trimEnd(rest);
  if (rest.length > 0) lines.push(rest);
  return lines;
}

export function lineContentWidth(line: ClusterItem[]): number {
  return line.reduce((sum, it) => sum + it.width, 0);
}

// Strict feasibility: fits the line budget with no overflowing line.
// A line that holds exactly one unbreakable group is exempt: an
// unbreakable entity wider than the column overflows whole instead of
// being dropped or split.
export function fitsInLines(items: ClusterItem[], lineWidth: number, maxLines: number): boolean {
  const lines = wrapLines(items, lineWidth);
  if (lines.length > maxLines) return false;
  for (const line of lines) {
    if (lineContentWidth(line) <= lineWidth) continue;
    const group = line[0].atomicId;
    const singleGroup = group !== 0 && line.every((it) => it.atomicId === group);
    if (!singleGroup) return false;
  }
  return true;
}

// ---------- truncation ----------

function cutLegal(items: ClusterItem[], pos: number): boolean {
  if (pos <= 0 || pos >= items.length) return true;
  const prev = items[pos - 1];
  const next = items[pos];
  return !(prev.atomicId !== 0 && prev.atomicId === next.atomicId);
}

function keptStats(seq: ClusterItem[]): [number, number, number] {
  let req = 0;
  let key = 0;
  let total = 0;
  for (const it of seq) {
    if (it.required) req++;
    if (it.key) key++;
    if (!isSpace(it)) total++; // whitespace may vanish at render time
  }
  return [req, key, total];
}

interface TruncPlan {
  kept: ClusterItem[];      // logical sequence including the ellipsis item
  hasEllipsis: boolean;
  conflicts: ConflictInfo[];
}

function planTruncation(
  items: ClusterItem[],
  opts: LayoutOptions,
  table: MetricTable,
  paraDir: Dir,
): TruncPlan | null {
  const eWidth = ellipsisWidth(table);
  const capacity = opts.width * opts.maxLines;

  const keptParts = (a: number, b: number): [ClusterItem[], ClusterItem[]] => {
    const prefix = items.slice(0, a);
    const suffix = b > 0 ? items.slice(items.length - b) : [];
    trimStart(prefix); // leading spaces of the block never render
    trimEnd(prefix);
    trimStart(suffix);
    trimEnd(suffix); // trailing spaces are dropped at render time
    return [prefix, suffix];
  };

  const buildKept = (a: number, b: number): ClusterItem[] => {
    const [prefix, suffix] = keptParts(a, b);
    const ellipsis = makeEllipsisItem(paraDir);
    ellipsis.width = eWidth;
    return [...prefix, ellipsis, ...suffix];
  };

  const feasible = (a: number, b: number): boolean =>
    fitsInLines(buildKept(a, b), opts.width, opts.maxLines);

  const legalCut = (pos: number): boolean => cutLegal(items, pos);

  if (opts.mode === 'end') {
    for (let a = items.length; a >= 0; a--) {
      if (!legalCut(a)) continue;
      if (feasible(a, 0)) return { kept: buildKept(a, 0), hasEllipsis: true, conflicts: [] };
    }
    return { kept: buildKept(0, 0), hasEllipsis: true, conflicts: [] };
  }

  const scoreOf = (a: number, b: number): number[] => {
    const [prefix, suffix] = keptParts(a, b);
    const kept = [...prefix, ...suffix];
    const [req, key, total] = keptStats(kept);
    if (opts.mode === 'key') return [req, key, total];
    return [total, -Math.abs(a - b)]; // middle: most content, then balanced
  };

  const best = exhaustiveBest(items.length, legalCut, feasible, scoreOf);
  if (!best) return null;

  const conflicts: ConflictInfo[] = [];
  if (opts.mode === 'key') {
    const totalReq = keptStats(items)[0];
    const [bestPrefix, bestSuffix] = keptParts(best.a, best.b);
    const keptClusters = [...bestPrefix, ...bestSuffix];
    const keptReq = keptStats(keptClusters)[0];
    if (keptReq < totalReq) {
      const droppedIds = new Set<string>();
      const keptSet = new Set(keptClusters);
      let requiredWidth = 0;
      for (const it of items) {
        if (it.required) {
          requiredWidth += it.width;
          if (!keptSet.has(it)) droppedIds.add(it.keyId ?? '(anonymous)');
        }
      }
      conflicts.push({
        type: 'key-overflow',
        keyIds: [...droppedIds],
        requiredWidth,
        capacity: capacity - eWidth,
        message: `required key fragments need ${requiredWidth}px but only ${capacity - eWidth}px is available; dropped: ${[...droppedIds].join(', ')}`,
      });
    }
  }
  return { kept: buildKept(best.a, best.b), hasEllipsis: true, conflicts };
}

// ---------- visual lines, links ----------

function visualize(
  lines: ClusterItem[][],
  paraDir: Dir,
): { visual: VisualItem[][]; ellipsis?: { line: number; x: number; width: number } } {
  const visual: VisualItem[][] = [];
  let ellipsis: { line: number; x: number; width: number } | undefined;
  for (let li = 0; li < lines.length; li++) {
    const ordered = reorderVisual(lines[li], (it) => levelOf(it.dir, paraDir));
    let x = 0;
    const row: VisualItem[] = [];
    for (const it of ordered) {
      row.push({
        kind: it.kind,
        text: it.text,
        x,
        width: it.width,
        line: li,
        dir: it.dir,
        srcStart: it.srcStart,
        srcEnd: it.srcEnd,
        linkId: it.linkId,
        href: it.href,
        key: it.key,
        keyId: it.keyId,
      });
      if (it.kind === 'ellipsis') ellipsis = { line: li, x, width: it.width };
      x += it.width;
    }
    visual.push(row);
  }
  return { visual, ellipsis };
}

function collectLinks(visual: VisualItem[][]): LinkFragment[] {
  const byLink = new Map<string, { href: string; items: VisualItem[] }>();
  for (const row of visual) {
    for (const it of row) {
      if (!it.linkId) continue;
      let entry = byLink.get(it.linkId);
      if (!entry) {
        entry = { href: it.href ?? '', items: [] };
        byLink.set(it.linkId, entry);
      }
      entry.items.push(it);
    }
  }
  const out: LinkFragment[] = [];
  for (const [linkId, entry] of byLink) {
    const sorted = entry.items.slice().sort((p, q) => p.srcStart - q.srcStart);
    const ranges: Array<{ start: number; end: number }> = [];
    for (const it of sorted) {
      const last = ranges[ranges.length - 1];
      if (last && it.srcStart <= last.end) last.end = Math.max(last.end, it.srcEnd);
      else ranges.push({ start: it.srcStart, end: it.srcEnd });
    }
    const byLine = new Map<number, VisualItem[]>();
    for (const it of entry.items) {
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
    out.push({ linkId, href: entry.href, ranges, rects });
  }
  return out;
}

// ---------- entry point ----------

export function layoutBlock(
  items: ClusterItem[],
  paraDir: Dir,
  source: string,
  opts: LayoutOptions,
  table: MetricTable,
): LayoutResult {
  measure(items, table, '…');
  const eWidth = ellipsisWidth(table);
  const maxLines = Math.max(1, opts.maxLines);

  const full = wrapLines(items, opts.width);
  if (full.length <= maxLines) {
    const { visual } = visualize(full, paraDir);
    return {
      paraDir,
      lines: visual,
      truncated: false,
      conflicts: [],
      linkFragments: collectLinks(visual),
      metricVersion: table.version,
      source,
    };
  }

  // Ellipsis wider than a line: explicit degradation, no ellipsis placed.
  if (eWidth > opts.width) {
    const keptLines = full.slice(0, maxLines);
    const { visual } = visualize(keptLines, paraDir);
    return {
      paraDir,
      lines: visual,
      truncated: true,
      degraded: {
        reason: 'ellipsis-too-wide',
        ellipsisWidth: eWidth,
        lineWidth: opts.width,
        message: `ellipsis (${eWidth}px) is wider than the line (${opts.width}px); content clipped without ellipsis`,
      },
      conflicts: [],
      linkFragments: collectLinks(visual),
      metricVersion: table.version,
      source,
    };
  }

  const plan = planTruncation(items, { ...opts, maxLines }, table, paraDir);
  const kept = plan ? plan.kept : [makeEllipsisItem(paraDir)];
  if (!plan) kept[0].width = eWidth;
  const lines = wrapLines(kept, opts.width).slice(0, maxLines);
  const { visual, ellipsis } = visualize(lines, paraDir);
  return {
    paraDir,
    lines: visual,
    truncated: true,
    conflicts: plan ? plan.conflicts : [],
    linkFragments: collectLinks(visual),
    metricVersion: table.version,
    source,
    ellipsis,
  };
}
