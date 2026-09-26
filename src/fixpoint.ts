import type { Document, GlyphMetrics, PageConfig } from "./model.ts";
import { paginate, type PaginationResult } from "./paginate.ts";
import { validateLayout } from "./validate.ts";

export type FixpointOutcome =
  | { ok: true; result: PaginationResult; iterations: number }
  | { ok: false; reason: string; iterations: number };

export function resolveFixpoint(
  doc: Document,
  metrics: GlyphMetrics,
  cfg: PageConfig,
  maxIterations = 8,
): FixpointOutcome {
  const seen = new Set<string>();
  for (let iter = 1; iter <= maxIterations; iter++) {
    const result = paginate(doc, metrics, cfg);
    if (!result.feasible) {
      return { ok: false, reason: `infeasible: ${result.error}`, iterations: iter };
    }
    const problems = validateLayout(result, doc, metrics, cfg);
    const signature = JSON.stringify(
      result.pages.map((p) => [p.start, p.end, p.footnoteIds.join(",")]),
    );
    if (problems.length === 0) {
      return { ok: true, result, iterations: iter };
    }
    if (seen.has(signature)) {
      return {
        ok: false,
        reason: `no stable fixpoint; unresolved problems: ${problems.join("; ")}`,
        iterations: iter,
      };
    }
    seen.add(signature);
  }
  return {
    ok: false,
    reason: `layout did not converge within ${maxIterations} iterations`,
    iterations: maxIterations,
  };
}
