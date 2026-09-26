import type { Document, GlyphMetrics, PageConfig } from "./model.ts";
import { evaluatePage, prepare, type PaginationResult } from "./paginate.ts";

export function validateLayout(
  result: PaginationResult,
  doc: Document,
  metrics: GlyphMetrics,
  cfg: PageConfig,
): string[] {
  const problems: string[] = [];
  if (!result.feasible) return [`layout is infeasible: ${result.error ?? "unknown"}`];
  const prepared = prepare(doc, metrics, cfg);
  if ("error" in prepared) return [`document does not prepare: ${prepared.error}`];
  const { ctx } = prepared;
  const items = result.items;

  let expect = 0;
  for (const page of result.pages) {
    if (page.start !== expect) {
      problems.push(`page ${page.index} starts at item ${page.start}, expected ${expect}`);
    }
    if (page.end <= page.start) {
      problems.push(`page ${page.index} is empty (blank page)`);
    }
    expect = page.end;
  }
  if (expect !== items.length) {
    problems.push(`pages cover ${expect} of ${items.length} items`);
  }

  const degraded = new Set(
    result.degradations.map((d) => `${d.page}:${d.constraint}:${d.nodeId}`),
  );
  for (const page of result.pages) {
    const ev = evaluatePage(items, page.start, page.end, ctx);
    if (!ev.overflow && ev.bodyUsed + ev.footnoteHeight > cfg.pageHeight) {
      problems.push(
        `page ${page.index} exceeds capacity: ${ev.bodyUsed + ev.footnoteHeight} > ${cfg.pageHeight}`,
      );
    }
    for (const v of ev.violations) {
      const nodeId = v === "orphans" ? items[page.start].nodeId : items[page.end - 1].nodeId;
      if (!degraded.has(`${page.index}:${v}:${nodeId}`)) {
        problems.push(`page ${page.index} silently violates ${v} on node ${nodeId}`);
      }
    }
  }

  const refPage = new Map<string, number>();
  for (const page of result.pages) {
    for (let k = page.start; k < page.end; k++) {
      const it = items[k];
      if (it.type === "line") {
        for (const ref of it.footnoteRefs) {
          if (!refPage.has(ref)) refPage.set(ref, page.index);
        }
      }
    }
  }
  for (const page of result.pages) {
    for (const id of page.footnoteIds) {
      if (refPage.get(id) !== page.index) {
        problems.push(
          `footnote ${id} attributed to page ${page.index} but its reference is on page ${refPage.get(id)}`,
        );
      }
    }
  }
  for (const [ref, pageIndex] of refPage) {
    const page = result.pages[pageIndex];
    if (!page || !page.footnoteIds.includes(ref)) {
      problems.push(`footnote ${ref} referenced on page ${pageIndex} is not attributed there`);
    }
  }
  return problems;
}
