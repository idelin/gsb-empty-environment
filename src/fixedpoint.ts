import type { Document, GlyphMetrics } from './types.ts';
import { breakParagraph } from './linebreak.ts';
import { paginate, type Layout, type PageConfig } from './paginate.ts';

/** Footnote heights may depend on where references land (page assignment).
 *  The oracle is re-queried after each trial layout; iteration stops to a
 *  fixed point or reports that none exists. */
export interface FootnoteOracle {
  heights(context: {
    assignment: Record<string, number> | null;
    iteration: number;
  }): Record<string, number>;
}

export function constantOracle(heights: Record<string, number>): FootnoteOracle {
  return { heights: () => ({ ...heights }) };
}

/** Derives footnote heights from footnote text via fixture glyph metrics. */
export function textMetricsOracle(
  doc: Document,
  metrics: GlyphMetrics,
  footnoteLineWidth: number,
): FootnoteOracle {
  return {
    heights: () => {
      const heights: Record<string, number> = {};
      for (const [id, fn] of Object.entries(doc.footnotes)) {
        heights[id] = breakParagraph(fn.text, metrics, footnoteLineWidth).lines.length *
          metrics.lineHeight;
      }
      return heights;
    },
  };
}

export type FixedPointResult =
  | { ok: true; layout: Layout; iterations: number; assignment: Record<string, number> }
  | { ok: false; reason: string; history: string[] };

export function paginateToFixedPoint(
  doc: Document,
  metrics: GlyphMetrics,
  config: PageConfig,
  oracle: FootnoteOracle,
  maxIterations = 16,
): FixedPointResult {
  const seen = new Map<string, number>();
  const history: string[] = [];
  let heights = oracle.heights({ assignment: null, iteration: 0 });
  const signature = (h: Record<string, number>) => JSON.stringify(Object.entries(h).sort());

  for (let iter = 1; iter <= maxIterations; iter++) {
    const sig = signature(heights);
    if (seen.has(sig)) {
      return {
        ok: false,
        reason: `no fixed point: footnote heights oscillate (signature of iteration ${iter} already seen at iteration ${seen.get(sig)})`,
        history,
      };
    }
    seen.set(sig, iter);
    history.push(`iteration ${iter}: heights=${sig}`);

    const layout = paginate(doc, metrics, config, heights);
    const assignment: Record<string, number> = {};
    for (const page of layout.pages) {
      for (const fn of page.footnotes) assignment[fn.id] = page.index;
    }
    const next = oracle.heights({ assignment, iteration: iter });
    if (signature(next) === sig) {
      return { ok: true, layout, iterations: iter, assignment };
    }
    heights = next; // roll back the trial layout and retry with corrected heights
  }
  return { ok: false, reason: `no fixed point within ${maxIterations} iterations`, history };
}

export interface Correction {
  footnoteText?: Record<string, string>;
}

/** Local content correction: returns a new document with the patch applied.
 *  Re-running the fixed point afterwards lets a taller footnote cascade and
 *  push body text to later pages. */
export function applyCorrection(doc: Document, correction: Correction): Document {
  const footnotes = { ...doc.footnotes };
  for (const [id, text] of Object.entries(correction.footnoteText ?? {})) {
    if (footnotes[id]) footnotes[id] = { text };
  }
  return { ...doc, footnotes };
}
