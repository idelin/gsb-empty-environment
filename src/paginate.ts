import type { Document, GlyphMetrics } from './types.ts';
import { breakParagraph } from './linebreak.ts';

export interface PageConfig {
  pageCapacity: number;
  minOrphanLines: number; // min paragraph lines at the bottom of a page
  minWidowLines: number; // min paragraph lines carried to the top of the next page
  breakPenalty: number; // cost of any page break
  paragraphSplitPenalty: number; // extra cost when a break splits a paragraph
  blankCostWeight: number; // cost = weight * slack^2 per page
  footnoteSeparatorHeight: number; // charged once per page that hosts footnotes
  lastPageSlackFree: boolean;
}

export const DEFAULT_PAGE_CONFIG: PageConfig = {
  pageCapacity: 100,
  minOrphanLines: 2,
  minWidowLines: 2,
  breakPenalty: 50,
  paragraphSplitPenalty: 10,
  blankCostWeight: 1,
  footnoteSeparatorHeight: 4,
  lastPageSlackFree: true,
};

export type Atom =
  | { kind: 'heading'; blockId: string; height: number }
  | {
      kind: 'line';
      blockId: string;
      lineIndex: number;
      lineCount: number;
      height: number;
      footnoteRefs: string[];
    }
  | { kind: 'block'; blockId: string; height: number };

export interface Degradation {
  kind: 'block-overflow' | 'footnote-overflow' | 'constraint-relaxed' | 'long-word' | 'undefined-footnote';
  message: string;
}

export interface Page {
  index: number;
  atoms: Atom[];
  bodyHeight: number;
  footnotes: { id: string; height: number }[];
  footnoteSeparator: number;
  used: number;
  slack: number;
  overflow: boolean;
  relaxedConstraints: string[];
}

export interface Layout {
  ok: true;
  pages: Page[];
  cost: number;
  degradations: Degradation[];
  stats: { candidatesTried: number; candidatesPruned: number };
}

export function buildAtoms(
  doc: Document,
  metrics: GlyphMetrics,
): { atoms: Atom[]; degradations: Degradation[] } {
  const atoms: Atom[] = [];
  const degradations: Degradation[] = [];
  for (const block of doc.blocks) {
    if (block.kind === 'heading') {
      atoms.push({ kind: 'heading', blockId: block.id, height: metrics.headingHeight });
    } else if (block.kind === 'block') {
      atoms.push({ kind: 'block', blockId: block.id, height: block.height });
    } else {
      const { lines, overflowWords } = breakParagraph(block.text, metrics, metrics.maxLineWidth);
      for (const word of overflowWords) {
        degradations.push({
          kind: 'long-word',
          message: `word "${word}" in ${block.id} exceeds line width ${metrics.maxLineWidth}; set on its own overflowing line`,
        });
      }
      lines.forEach((line, i) => {
        atoms.push({
          kind: 'line',
          blockId: block.id,
          lineIndex: i,
          lineCount: lines.length,
          height: metrics.lineHeight,
          footnoteRefs: line.footnoteRefs,
        });
      });
    }
  }
  return { atoms, degradations };
}

/** Attribute each footnote id to the atom of its first reference. */
export function attributeFootnotes(atoms: Atom[]): string[][] {
  const seen = new Set<string>();
  return atoms.map((atom) => {
    if (atom.kind !== 'line') return [];
    const fresh: string[] = [];
    for (const id of atom.footnoteRefs) {
      if (!seen.has(id)) {
        seen.add(id);
        fresh.push(id);
      }
    }
    return fresh;
  });
}

/** Returns why a break between atoms a (page bottom) and b (page top) is
 *  forbidden, or null when the break is allowed. */
export function breakProblem(a: Atom, b: Atom, config: PageConfig): string | null {
  if (a.kind === 'heading') return 'keep-with-next';
  if (a.kind === 'line' && b.kind === 'line' && a.blockId === b.blockId) {
    const before = a.lineIndex + 1;
    const after = a.lineCount - before;
    if (before < config.minOrphanLines || after < config.minWidowLines) return 'widow-orphan';
  }
  return null;
}

export function pageBreakCost(
  slack: number,
  isLast: boolean,
  splitsParagraph: boolean,
  config: PageConfig,
): number {
  let cost = isLast && config.lastPageSlackFree ? 0 : config.blankCostWeight * slack * slack;
  if (!isLast) {
    cost += config.breakPenalty;
    if (splitsParagraph) cost += config.paragraphSplitPenalty;
  }
  return cost;
}

interface PageSpec {
  start: number;
  end: number;
  overflow: boolean;
  relaxed: string[];
}

interface Entry {
  cost: number;
  pages: PageSpec[];
  degradations: Degradation[];
}

export function paginate(
  doc: Document,
  metrics: GlyphMetrics,
  config: PageConfig,
  footnoteHeights: Record<string, number>,
): Layout {
  const { atoms, degradations } = buildAtoms(doc, metrics);
  const newFootnotes = attributeFootnotes(atoms);
  for (const ids of newFootnotes) {
    for (const id of ids) {
      if (footnoteHeights[id] === undefined) {
        degradations.push({
          kind: 'undefined-footnote',
          message: `footnote "${id}" is referenced but has no height entry; treated as height 0`,
        });
      }
    }
  }
  const inner = paginateAtoms(atoms, newFootnotes, footnoteHeights, config);
  return { ...inner, degradations: [...degradations, ...inner.degradations] };
}

/**
 * Backtracking search (memoized DP) over page-break positions. Candidate
 * breaks are tried and rolled back when they dead-end; constraints that
 * cannot be satisfied produce explicit degradation records, never silent
 * violations. Pages always contain at least one atom (no blank pages).
 */
export function paginateAtoms(
  atoms: Atom[],
  newFootnotes: string[][],
  heights: Record<string, number>,
  config: PageConfig,
): Layout {
  const n = atoms.length;
  const stats = { candidatesTried: 0, candidatesPruned: 0 };
  const memo: (Entry | undefined)[] = new Array(n + 1).fill(undefined);

  const dp = (i: number): Entry => {
    if (i >= n) return { cost: 0, pages: [], degradations: [] };
    const cached = memo[i];
    if (cached !== undefined) return cached;

    let best: Entry | null = null;
    let overflowEntry: Entry | null = null;
    let used = 0;
    let footnoteSum = 0;
    let separator = 0;
    const fnSeen = new Set<string>();

    for (let j = i + 1; j <= n; j++) {
      const atom = atoms[j - 1];
      for (const id of newFootnotes[j - 1]) {
        if (!fnSeen.has(id)) {
          fnSeen.add(id);
          footnoteSum += heights[id] ?? 0;
        }
      }
      if (fnSeen.size > 0) separator = config.footnoteSeparatorHeight;
      const total = used + atom.height + footnoteSum + separator;
      stats.candidatesTried++;

      if (total > config.pageCapacity) {
        stats.candidatesPruned++;
        if (j === i + 1) {
          // A single atom (plus its footnotes) exceeds the page: place it
          // alone, flag overflow, and report the degradation explicitly.
          const rest = dp(j);
          const kind = atom.height > config.pageCapacity ? 'block-overflow' : 'footnote-overflow';
          const message =
            kind === 'block-overflow'
              ? `unbreakable content "${atom.blockId}" (height ${atom.height}) exceeds page capacity ${config.pageCapacity}; placed with overflow`
              : `footnotes of "${atom.blockId}" do not fit next to their reference within page capacity ${config.pageCapacity}; placed with overflow`;
          overflowEntry = {
            cost: pageBreakCost(0, j === n, false, config) + rest.cost,
            pages: [{ start: i, end: j, overflow: true, relaxed: [] }, ...rest.pages],
            degradations: [{ kind, message }, ...rest.degradations],
          };
        }
        break;
      }

      used += atom.height;
      const isLast = j === n;
      if (!isLast && breakProblem(atoms[j - 1], atoms[j], config) !== null) continue;

      const rest = isLast ? { cost: 0, pages: [], degradations: [] } : dp(j);
      const splitsParagraph =
        !isLast &&
        atoms[j - 1].kind === 'line' &&
        atoms[j].kind === 'line' &&
        atoms[j - 1].blockId === atoms[j].blockId;
      const candidate: Entry = {
        cost: pageBreakCost(config.pageCapacity - total, isLast, splitsParagraph, config) + rest.cost,
        pages: [{ start: i, end: j, overflow: false, relaxed: [] }, ...rest.pages],
        degradations: rest.degradations,
      };
      if (!best || candidate.cost < best.cost) best = candidate;
    }

    if (!best) {
      if (overflowEntry) {
        best = overflowEntry;
      } else {
        // No constraint-satisfying break exists from i: relax exactly one
        // constraint and report it. This path still never emits a blank page.
        const atom = atoms[i];
        const rest = dp(i + 1);
        const problem =
          i + 1 < n ? breakProblem(atom, atoms[i + 1], config) ?? 'unbreakable' : 'unbreakable';
        const slack = Math.max(0, config.pageCapacity - atom.height);
        best = {
          cost: pageBreakCost(slack, i + 1 === n, false, config) + rest.cost,
          pages: [{ start: i, end: i + 1, overflow: false, relaxed: [problem] }, ...rest.pages],
          degradations: [
            {
              kind: 'constraint-relaxed',
              message: `${problem} constraint cannot be satisfied for "${atom.blockId}" within page capacity ${config.pageCapacity}; relaxed explicitly`,
            },
            ...rest.degradations,
          ],
        };
      }
    }

    memo[i] = best;
    return best;
  };

  const entry = dp(0);
  const pages: Page[] = entry.pages.map((spec, index) => {
    const pageAtoms = atoms.slice(spec.start, spec.end);
    const bodyHeight = pageAtoms.reduce((sum, a) => sum + a.height, 0);
    const fnIds: string[] = [];
    const seen = new Set<string>();
    for (let k = spec.start; k < spec.end; k++) {
      for (const id of newFootnotes[k]) {
        if (!seen.has(id)) {
          seen.add(id);
          fnIds.push(id);
        }
      }
    }
    const footnotes = fnIds.map((id) => ({ id, height: heights[id] ?? 0 }));
    const footnoteSeparator = fnIds.length > 0 ? config.footnoteSeparatorHeight : 0;
    const usedHeight =
      bodyHeight + footnotes.reduce((sum, f) => sum + f.height, 0) + footnoteSeparator;
    return {
      index,
      atoms: pageAtoms,
      bodyHeight,
      footnotes,
      footnoteSeparator,
      used: usedHeight,
      slack: config.pageCapacity - usedHeight,
      overflow: spec.overflow,
      relaxedConstraints: spec.relaxed,
    };
  });
  return { ok: true, pages, cost: entry.cost, degradations: entry.degradations, stats };
}
