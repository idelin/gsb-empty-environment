import type { Document, GlyphMetrics, PageConfig } from "./model.ts";
import { flatten, footnoteHeights, type Item } from "./items.ts";

export type ConstraintKind = "keep-with-next" | "widows" | "orphans" | "block-overflow";

export interface Degradation {
  constraint: ConstraintKind;
  page: number;
  nodeId: string;
  detail: string;
}

export interface PageLayout {
  index: number;
  start: number;
  end: number;
  bodyUsed: number;
  footnoteHeight: number;
  footnoteIds: string[];
  overflow: boolean;
  violations: ConstraintKind[];
}

export interface PaginationResult {
  feasible: boolean;
  error?: string;
  items: Item[];
  pages: PageLayout[];
  cost: number;
  degradations: Degradation[];
  level: number;
  warnings: string[];
}

export interface Ctx {
  cfg: PageConfig;
  footnoteHeights: Map<string, number>;
}

export const RELAX_ORDER: ConstraintKind[] = ["keep-with-next", "widows", "orphans"];

export const VIOLATION_PENALTY: Record<string, number> = {
  "keep-with-next": 400,
  widows: 600,
  orphans: 600,
};

export const OVERFLOW_PENALTY = 100_000;

export interface PageEval {
  fits: boolean;
  overflow: boolean;
  bodyUsed: number;
  footnoteHeight: number;
  footnoteIds: string[];
  violations: ConstraintKind[];
  slack: number;
}

export function evaluatePage(
  items: Item[],
  start: number,
  end: number,
  ctx: Ctx,
): PageEval {
  let bodyUsed = 0;
  let foot = 0;
  const footIds: string[] = [];
  for (let k = start; k < end; k++) {
    const it = items[k];
    bodyUsed += it.height;
    if (it.type === "line") {
      for (const ref of it.footnoteRefs) {
        if (!footIds.includes(ref)) {
          footIds.push(ref);
          foot += ctx.footnoteHeights.get(ref) ?? 0;
        }
      }
    }
  }
  const separator = footIds.length > 0 ? ctx.cfg.footnoteSeparatorHeight : 0;
  const footnoteTotal = foot + separator;
  const total = bodyUsed + footnoteTotal;
  if (total > ctx.cfg.pageHeight) {
    const single = items[start];
    if (end === start + 1 && single.type === "block") {
      return {
        fits: true,
        overflow: true,
        bodyUsed,
        footnoteHeight: footnoteTotal,
        footnoteIds: footIds,
        violations: ["block-overflow"],
        slack: 0,
      };
    }
    return {
      fits: false,
      overflow: false,
      bodyUsed,
      footnoteHeight: footnoteTotal,
      footnoteIds: footIds,
      violations: [],
      slack: 0,
    };
  }
  const violations: ConstraintKind[] = [];
  const first = items[start];
  if (first.type === "line" && first.lineIndex > 0 && first.lineIndex < ctx.cfg.orphans) {
    violations.push("orphans");
  }
  const last = items[end - 1];
  if (last.type === "line" && last.lineIndex < last.lineCount - 1) {
    const remaining = last.lineCount - 1 - last.lineIndex;
    if (remaining < ctx.cfg.widows) violations.push("widows");
  }
  if (last.type === "heading" && ctx.cfg.keepWithNext) {
    violations.push("keep-with-next");
  }
  return {
    fits: true,
    overflow: false,
    bodyUsed,
    footnoteHeight: footnoteTotal,
    footnoteIds: footIds,
    violations,
    slack: ctx.cfg.pageHeight - total,
  };
}

export function allowedForLevel(level: number): Set<string> {
  return new Set(RELAX_ORDER.slice(0, level));
}

export function hardViolations(ev: PageEval, allowed: Set<string>): ConstraintKind[] {
  return ev.violations.filter((v) => v !== "block-overflow" && !allowed.has(v));
}

export function pageCost(
  items: Item[],
  j: number,
  ev: PageEval,
  ctx: Ctx,
  allowed: Set<string>,
): number {
  let cost = 0;
  if (j !== items.length) cost += ctx.cfg.whitespaceWeight * ev.slack * ev.slack;
  const lastItem = items[j - 1];
  if (lastItem.type === "line" && lastItem.lineIndex < lastItem.lineCount - 1) {
    cost += ctx.cfg.paragraphSplitPenalty;
  }
  for (const v of ev.violations) {
    if (v === "block-overflow") cost += OVERFLOW_PENALTY;
    else if (allowed.has(v)) cost += VIOLATION_PENALTY[v];
  }
  return cost;
}

export interface Prepared {
  items: Item[];
  ctx: Ctx;
  warnings: string[];
}

export function prepare(
  doc: Document,
  metrics: GlyphMetrics,
  cfg: PageConfig,
): Prepared | { error: string } {
  const items = flatten(doc, metrics, cfg.lineWidth);
  const heights = footnoteHeights(doc, metrics, cfg.lineWidth);
  const warnings: string[] = [];
  const referenced = new Set<string>();
  for (const it of items) {
    if (it.type === "line") for (const r of it.footnoteRefs) referenced.add(r);
  }
  for (const r of referenced) {
    if (!heights.has(r)) return { error: `unknown footnote reference: ${r}` };
  }
  for (const fn of doc.footnotes) {
    if (!referenced.has(fn.id)) warnings.push(`footnote ${fn.id} defined but never referenced`);
  }
  for (const it of items) {
    if (it.type !== "block" && it.height > cfg.pageHeight) {
      return {
        error: `item of node ${it.nodeId} (height ${it.height}) exceeds page capacity ${cfg.pageHeight}`,
      };
    }
  }
  return { items, ctx: { cfg, footnoteHeights: heights }, warnings };
}

export interface LevelSolution {
  cost: number;
  breaks: number[];
}

export function solveLevel(
  items: Item[],
  ctx: Ctx,
  level: number,
  from = 0,
): LevelSolution | null {
  const allowed = allowedForLevel(level);
  const memo = new Map<number, { cost: number; next: number } | null>();
  const solve = (i: number): { cost: number; next: number } | null => {
    if (i >= items.length) return { cost: 0, next: i };
    if (memo.has(i)) return memo.get(i)!;
    let best: { cost: number; next: number } | null = null;
    for (let j = i + 1; j <= items.length; j++) {
      const ev = evaluatePage(items, i, j, ctx);
      if (!ev.fits) break;
      if (hardViolations(ev, allowed).length > 0) continue;
      const rest = solve(j);
      if (!rest) continue;
      const total = pageCost(items, j, ev, ctx, allowed) + rest.cost;
      if (!best || total < best.cost) best = { cost: total, next: j };
    }
    memo.set(i, best);
    return best;
  };
  const first = solve(from);
  if (!first) return null;
  const breaks: number[] = [];
  let i = from;
  while (i < items.length) {
    const step = memo.get(i);
    if (!step) return null;
    breaks.push(step.next);
    i = step.next;
  }
  return { cost: first.cost, breaks };
}

function describeViolation(
  v: ConstraintKind,
  items: Item[],
  start: number,
  end: number,
  ctx: Ctx,
): string {
  if (v === "orphans") {
    const first = items[start];
    const before = first.type === "line" ? first.lineIndex : 0;
    return `paragraph ${first.nodeId} leaves only ${before} line(s) on the previous page (min orphans=${ctx.cfg.orphans})`;
  }
  if (v === "widows") {
    const last = items[end - 1];
    const remaining = last.type === "line" ? last.lineCount - 1 - last.lineIndex : 0;
    return `paragraph ${last.nodeId} carries only ${remaining} line(s) to the next page (min widows=${ctx.cfg.widows})`;
  }
  if (v === "keep-with-next") {
    return `heading ${items[end - 1].nodeId} is the last element on its page`;
  }
  const it = items[start];
  return `unbreakable block ${it.nodeId} (height ${it.height}) exceeds page capacity ${ctx.cfg.pageHeight}`;
}

export function paginate(
  doc: Document,
  metrics: GlyphMetrics,
  cfg: PageConfig,
): PaginationResult {
  const prepared = prepare(doc, metrics, cfg);
  if ("error" in prepared) {
    return {
      feasible: false,
      error: prepared.error,
      items: [],
      pages: [],
      cost: 0,
      degradations: [],
      level: -1,
      warnings: [],
    };
  }
  const { items, ctx, warnings } = prepared;
  for (let level = 0; level <= RELAX_ORDER.length; level++) {
    const solved = solveLevel(items, ctx, level);
    if (!solved) continue;
    const pages: PageLayout[] = [];
    const degradations: Degradation[] = [];
    let start = 0;
    solved.breaks.forEach((end, pageIndex) => {
      const ev = evaluatePage(items, start, end, ctx);
      pages.push({
        index: pageIndex,
        start,
        end,
        bodyUsed: ev.bodyUsed,
        footnoteHeight: ev.footnoteHeight,
        footnoteIds: ev.footnoteIds,
        overflow: ev.overflow,
        violations: ev.violations,
      });
      for (const v of ev.violations) {
        const nodeId = v === "orphans" ? items[start].nodeId : items[end - 1].nodeId;
        degradations.push({
          constraint: v,
          page: pageIndex,
          nodeId,
          detail: describeViolation(v, items, start, end, ctx),
        });
      }
      start = end;
    });
    return { feasible: true, items, pages, cost: solved.cost, degradations, level, warnings };
  }
  return {
    feasible: false,
    error: "no valid pagination found at any relaxation level",
    items,
    pages: [],
    cost: 0,
    degradations: [],
    level: -1,
    warnings,
  };
}
