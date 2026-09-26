import type { BlockNode, Document, GlyphMetrics, PageConfig } from "./model.ts";
import { flatten, footnoteHeights, type Item } from "./items.ts";
import {
  allowedForLevel,
  evaluatePage,
  pageCost,
  paginate,
  prepare,
  solveLevel,
  type PageLayout,
  type PaginationResult,
} from "./paginate.ts";
import { validateLayout } from "./validate.ts";
import { resolveFixpoint } from "./fixpoint.ts";

export interface NodeEdit {
  nodeId: string;
  replace: BlockNode;
}

export function applyEdit(doc: Document, edit: NodeEdit): Document {
  return {
    nodes: doc.nodes.map((n) => (n.id === edit.nodeId ? edit.replace : n)),
    footnotes: doc.footnotes,
  };
}

export function applyFootnoteEdit(
  doc: Document,
  footnoteId: string,
  text: string,
): Document {
  return {
    nodes: doc.nodes,
    footnotes: doc.footnotes.map((f) => (f.id === footnoteId ? { ...f, text } : f)),
  };
}

function itemKey(it: Item): string {
  if (it.type === "line") {
    return `line:${it.nodeId}:${it.lineIndex}/${it.lineCount}:${it.height}:${it.footnoteRefs.join("+")}`;
  }
  return `${it.type}:${it.nodeId}:${it.height}`;
}

export interface LocalUpdateOutcome {
  ok: boolean;
  reason?: string;
  result?: PaginationResult;
  reusedPages: number;
  fellBackToFull: boolean;
  iterations: number;
}

export function repaginateLocal(
  prevDoc: Document,
  prevResult: PaginationResult,
  nextDoc: Document,
  metrics: GlyphMetrics,
  cfg: PageConfig,
): LocalUpdateOutcome {
  const prepared = prepare(nextDoc, metrics, cfg);
  if ("error" in prepared) {
    return { ok: false, reason: prepared.error, reusedPages: 0, fellBackToFull: false, iterations: 0 };
  }
  const nextItems = prepared.items;

  const prevHeights = footnoteHeights(prevDoc, metrics, cfg.lineWidth);
  const nextHeights = prepared.ctx.footnoteHeights;
  const heightsStable =
    prevHeights.size === nextHeights.size &&
    [...prevHeights].every(([k, v]) => nextHeights.get(k) === v);

  let diverge = 0;
  if (heightsStable) {
    while (
      diverge < prevResult.items.length &&
      diverge < nextItems.length &&
      itemKey(prevResult.items[diverge]) === itemKey(nextItems[diverge])
    ) {
      diverge++;
    }
  }

  let reuseEnd = 0;
  let reusedPages = 0;
  for (const page of prevResult.pages) {
    if (page.end <= diverge) {
      reuseEnd = page.end;
      reusedPages++;
    } else {
      break;
    }
  }

  const level = Math.max(prevResult.level, 0);
  const solved = solveLevel(nextItems, prepared.ctx, level, reuseEnd);
  if (!solved) {
    const full = resolveFixpoint(nextDoc, metrics, cfg);
    if (!full.ok) {
      return { ok: false, reason: full.reason, reusedPages: 0, fellBackToFull: true, iterations: full.iterations };
    }
    return { ok: true, result: full.result, reusedPages: 0, fellBackToFull: true, iterations: full.iterations };
  }

  const allowed = allowedForLevel(level);
  const pages: PageLayout[] = [];
  let cost = 0;
  for (let idx = 0; idx < reusedPages; idx++) {
    const prev = prevResult.pages[idx];
    const ev = evaluatePage(nextItems, prev.start, prev.end, prepared.ctx);
    cost += pageCost(nextItems, prev.end, ev, prepared.ctx, allowed);
    pages.push({ ...prev, index: idx });
  }
  let start = reuseEnd;
  for (const end of solved.breaks) {
    const ev = evaluatePage(nextItems, start, end, prepared.ctx);
    cost += pageCost(nextItems, end, ev, prepared.ctx, allowed);
    pages.push({
      index: pages.length,
      start,
      end,
      bodyUsed: ev.bodyUsed,
      footnoteHeight: ev.footnoteHeight,
      footnoteIds: ev.footnoteIds,
      overflow: ev.overflow,
      violations: ev.violations,
    });
    start = end;
  }

  const degradations = pages.flatMap((page) =>
    page.violations.map((v) => ({
      constraint: v,
      page: page.index,
      nodeId: v === "orphans" ? nextItems[page.start].nodeId : nextItems[page.end - 1].nodeId,
      detail: `local re-pagination: ${v} on page ${page.index}`,
    })),
  );

  const result: PaginationResult = {
    feasible: true,
    items: nextItems,
    pages,
    cost,
    degradations,
    level,
    warnings: prepared.warnings,
  };

  const problems = validateLayout(result, nextDoc, metrics, cfg);
  if (problems.length > 0) {
    const full = resolveFixpoint(nextDoc, metrics, cfg);
    if (!full.ok) {
      return { ok: false, reason: full.reason, reusedPages: 0, fellBackToFull: true, iterations: full.iterations };
    }
    return { ok: true, result: full.result, reusedPages: 0, fellBackToFull: true, iterations: full.iterations };
  }

  const full = paginate(nextDoc, metrics, cfg);
  const sameBreaks =
    full.feasible &&
    full.pages.length === pages.length &&
    full.pages.every((p, i) => p.start === pages[i].start && p.end === pages[i].end);
  if (!sameBreaks) {
    const fixed = resolveFixpoint(nextDoc, metrics, cfg);
    if (!fixed.ok) {
      return { ok: false, reason: fixed.reason, reusedPages: 0, fellBackToFull: true, iterations: fixed.iterations };
    }
    return { ok: true, result: fixed.result, reusedPages: 0, fellBackToFull: true, iterations: fixed.iterations };
  }

  return { ok: true, result, reusedPages, fellBackToFull: false, iterations: 1 };
}
