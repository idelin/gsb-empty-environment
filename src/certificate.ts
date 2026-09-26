import type { Document, GlyphMetrics, PageConfig } from "./model.ts";
import {
  allowedForLevel,
  evaluatePage,
  hardViolations,
  pageCost,
  prepare,
  type Degradation,
  type PaginationResult,
} from "./paginate.ts";
import type { Item } from "./items.ts";

export interface PageCertificate {
  page: number;
  capacity: number;
  bodyUsed: number;
  footnoteHeight: number;
  footnoteIds: string[];
  itemCount: number;
  overflow: boolean;
  checks: string[];
}

export interface ExhaustiveReport {
  claimedCost: number;
  cheaperFound: number;
  nodesExplored: number;
  verified: boolean;
  truncated: boolean;
}

export interface Certificate {
  pages: PageCertificate[];
  totalCost: number;
  level: number;
  degradations: Degradation[];
  warnings: string[];
  exhaustive: ExhaustiveReport;
}

export function exhaustiveVerify(
  items: Item[],
  ctx: { cfg: PageConfig; footnoteHeights: Map<string, number> },
  level: number,
  claimedCost: number,
  cap = 2_000_000,
): { cheaperFound: number; nodesExplored: number; truncated: boolean } {
  const allowed = allowedForLevel(level);
  let cheaperFound = 0;
  let nodesExplored = 0;
  let truncated = false;
  const dfs = (i: number, acc: number): void => {
    if (truncated) return;
    if (acc >= claimedCost) return;
    nodesExplored++;
    if (nodesExplored > cap) {
      truncated = true;
      return;
    }
    if (i === items.length) {
      cheaperFound++;
      return;
    }
    for (let j = items.length; j > i; j--) {
      const ev = evaluatePage(items, i, j, ctx);
      if (!ev.fits) continue;
      if (hardViolations(ev, allowed).length > 0) continue;
      dfs(j, acc + pageCost(items, j, ev, ctx, allowed));
    }
  };
  dfs(0, 0);
  return { cheaperFound, nodesExplored, truncated };
}

function pageChecks(result: PaginationResult, pageIndex: number, cfg: PageConfig): string[] {
  const page = result.pages[pageIndex];
  const items = result.items;
  const checks: string[] = [];
  const total = page.bodyUsed + page.footnoteHeight;
  checks.push(
    page.overflow
      ? `capacity: OVERFLOW (${total} > ${cfg.pageHeight}, unbreakable block)`
      : `capacity: ${total} <= ${cfg.pageHeight} ok`,
  );
  const first = items[page.start];
  if (first.type === "line" && first.lineIndex > 0) {
    checks.push(
      `orphans: ${first.lineIndex} line(s) left behind (min ${cfg.orphans}) -> ${page.violations.includes("orphans") ? "DEGRADED (reported)" : "ok"}`,
    );
  } else {
    checks.push("orphans: n/a (no paragraph continues onto this page)");
  }
  const last = items[page.end - 1];
  if (last.type === "line" && last.lineIndex < last.lineCount - 1) {
    const remaining = last.lineCount - 1 - last.lineIndex;
    checks.push(
      `widows: ${remaining} line(s) carried over (min ${cfg.widows}) -> ${page.violations.includes("widows") ? "DEGRADED (reported)" : "ok"}`,
    );
  } else if (last.type === "heading") {
    checks.push(
      `keep-with-next: heading ends page -> ${page.violations.includes("keep-with-next") ? "DEGRADED (reported)" : "ok"}`,
    );
  } else {
    checks.push("widows/keep-with-next: n/a");
  }
  checks.push(
    page.footnoteIds.length > 0
      ? `footnotes: [${page.footnoteIds.join(", ")}] occupy ${page.footnoteHeight}`
      : "footnotes: none",
  );
  return checks;
}

export function buildCertificate(
  result: PaginationResult,
  doc: Document,
  metrics: GlyphMetrics,
  cfg: PageConfig,
  exhaustiveCap = 500_000,
): Certificate {
  const prepared = prepare(doc, metrics, cfg);
  if ("error" in prepared) throw new Error(prepared.error);
  const exhaustiveRaw = exhaustiveVerify(
    prepared.items,
    prepared.ctx,
    result.level,
    result.cost,
    exhaustiveCap,
  );
  const pages: PageCertificate[] = result.pages.map((page, idx) => ({
    page: idx,
    capacity: cfg.pageHeight,
    bodyUsed: page.bodyUsed,
    footnoteHeight: page.footnoteHeight,
    footnoteIds: page.footnoteIds,
    itemCount: page.end - page.start,
    overflow: page.overflow,
    checks: pageChecks(result, idx, cfg),
  }));
  return {
    pages,
    totalCost: result.cost,
    level: result.level,
    degradations: result.degradations,
    warnings: result.warnings,
    exhaustive: {
      claimedCost: result.cost,
      cheaperFound: exhaustiveRaw.cheaperFound,
      nodesExplored: exhaustiveRaw.nodesExplored,
      verified: exhaustiveRaw.cheaperFound === 0 && !exhaustiveRaw.truncated,
      truncated: exhaustiveRaw.truncated,
    },
  };
}
