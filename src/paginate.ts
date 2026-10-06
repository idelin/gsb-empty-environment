/**
 * Fixed-height pagination for documents made of paragraphs, where each
 * paragraph supplies the height of every line it contains.
 *
 * Semantics:
 * - Line order is preserved; every page has the same fixed height.
 * - A paragraph may be split across pages, but every fragment produced by a
 *   split must contain at least two lines (no widows/orphans): at least two
 *   lines at the bottom of the page and at least two at the top of the next.
 * - A paragraph taller than a whole page is split across as many pages as
 *   needed; the two-line rule still applies to every fragment.
 * - A single line taller than the page is placed alone on its own page and
 *   allowed to overflow ("single column overflow"). It acts as a hard break:
 *   the surrounding paragraph fragments are treated as separate paragraphs.
 * - Objective (lexicographic):
 *   1. fewest pages that violate the two-line rule (only reachable for
 *      degenerate inputs where the rule is unsatisfiable, e.g. two lines of
 *      one paragraph cannot share a page);
 *   2. fewest pages;
 *   3. smallest sum of squared remaining heights over all pages
 *      (remaining = pageHeight - usedHeight), which balances page fill.
 */

export interface PageChunk {
  /** Index of the paragraph in the input document. */
  paragraph: number;
  /** Inclusive line index within the paragraph. */
  startLine: number;
  /** Exclusive line index within the paragraph. */
  endLine: number;
}

export interface Page {
  chunks: PageChunk[];
  usedHeight: number;
  /** True for a page holding a single line taller than the page. */
  overflow: boolean;
}

export interface PaginateResult {
  pages: Page[];
  pageCount: number;
  /** Number of pages forced to break the two-line rule (0 for sane inputs). */
  violations: number;
  /** Sum over pages of (pageHeight - usedHeight)^2. */
  remainingSquareSum: number;
}

/** [violations, pages, remainingSquareSum], compared lexicographically. */
type Cost = readonly [number, number, number];

const INF: Cost = [Infinity, Infinity, Infinity];

function costLt(a: Cost, b: Cost): boolean {
  if (a[0] !== b[0]) return a[0] < b[0];
  if (a[1] !== b[1]) return a[1] < b[1];
  return a[2] < b[2];
}

interface SegLine {
  para: number;
  line: number;
  height: number;
  /** Pseudo-paragraph id: original paragraph fragment between oversized lines. */
  pseudo: number;
}

type Block =
  | { kind: "lines"; lines: SegLine[] }
  | { kind: "oversized"; para: number; line: number; height: number };

function splitBlocks(
  paragraphs: ReadonlyArray<ReadonlyArray<number>>,
  pageHeight: number,
): Block[] {
  const blocks: Block[] = [];
  let current: SegLine[] = [];
  let pseudo = 0;
  let lastPara = -1;
  let fresh = true;
  const flush = (): void => {
    if (current.length > 0) {
      blocks.push({ kind: "lines", lines: current });
      current = [];
    }
  };
  paragraphs.forEach((lines, para) => {
    for (let line = 0; line < lines.length; line++) {
      const height = lines[line];
      if (height > pageHeight) {
        flush();
        blocks.push({ kind: "oversized", para, line, height });
        fresh = true;
      } else {
        if (fresh || para !== lastPara) {
          pseudo++;
          fresh = false;
        }
        current.push({ para, line, height, pseudo });
        lastPara = para;
      }
    }
  });
  flush();
  return blocks;
}

interface SegmentPage {
  start: number;
  end: number;
}

function paginateSegment(
  lines: SegLine[],
  pageHeight: number,
): { cost: Cost; pages: SegmentPage[] } {
  const n = lines.length;
  const pre = new Array<number>(n + 1).fill(0);
  for (let i = 0; i < n; i++) pre[i + 1] = pre[i] + lines[i].height;

  const pseudoStart = new Array<number>(n);
  const pseudoEnd = new Array<number>(n);
  const startByPseudo = new Map<number, number>();
  const endByPseudo = new Map<number, number>();
  for (let i = 0; i < n; i++) {
    if (!startByPseudo.has(lines[i].pseudo)) startByPseudo.set(lines[i].pseudo, i);
    endByPseudo.set(lines[i].pseudo, i + 1);
  }
  for (let i = 0; i < n; i++) {
    pseudoStart[i] = startByPseudo.get(lines[i].pseudo)!;
    pseudoEnd[i] = endByPseudo.get(lines[i].pseudo)!;
  }

  // A page covering lines [i, j) violates the two-line rule when a paragraph
  // fragment on it is a proper split fragment shorter than two lines.
  const violates = (i: number, j: number): boolean => {
    if (pseudoStart[i] < i && Math.min(j, pseudoEnd[i]) - i < 2) return true;
    if (pseudoEnd[j - 1] > j && j - Math.max(i, pseudoStart[j - 1]) < 2) return true;
    return false;
  };

  const memo: Cost[] = new Array<Cost>(n + 1);
  memo[n] = [0, 0, 0];
  const choice = new Array<number>(n).fill(-1);
  for (let i = n - 1; i >= 0; i--) {
    let best: Cost = INF;
    let bestJ = -1;
    for (let j = i + 1; j <= n; j++) {
      const h = pre[j] - pre[i];
      if (h > pageHeight) break;
      const rem = pageHeight - h;
      const tail = memo[j];
      const cand: Cost = [
        (violates(i, j) ? 1 : 0) + tail[0],
        1 + tail[1],
        rem * rem + tail[2],
      ];
      if (costLt(cand, best)) {
        best = cand;
        bestJ = j;
      }
    }
    memo[i] = best;
    choice[i] = bestJ;
  }

  const pages: SegmentPage[] = [];
  for (let i = 0; i < n; i = choice[i]) pages.push({ start: i, end: choice[i] });
  return { cost: memo[0], pages };
}

export function paginate(
  paragraphs: ReadonlyArray<ReadonlyArray<number>>,
  pageHeight: number,
): PaginateResult {
  if (!Number.isFinite(pageHeight) || pageHeight <= 0) {
    throw new RangeError(`pageHeight must be a positive finite number, got ${pageHeight}`);
  }
  paragraphs.forEach((lines, para) => {
    lines.forEach((height, line) => {
      if (!Number.isFinite(height) || height < 0) {
        throw new RangeError(`invalid line height ${height} at paragraph ${para}, line ${line}`);
      }
    });
  });

  const pages: Page[] = [];
  let violations = 0;
  let remainingSquareSum = 0;

  for (const block of splitBlocks(paragraphs, pageHeight)) {
    if (block.kind === "oversized") {
      pages.push({
        chunks: [{ paragraph: block.para, startLine: block.line, endLine: block.line + 1 }],
        usedHeight: block.height,
        overflow: true,
      });
      const rem = pageHeight - block.height;
      remainingSquareSum += rem * rem;
      continue;
    }
    const seg = paginateSegment(block.lines, pageHeight);
    violations += seg.cost[0];
    remainingSquareSum += seg.cost[2];
    for (const p of seg.pages) {
      const chunks: PageChunk[] = [];
      let usedHeight = 0;
      for (let k = p.start; k < p.end; k++) {
        const l = block.lines[k];
        usedHeight += l.height;
        const last = chunks[chunks.length - 1];
        if (last && last.paragraph === l.para && last.endLine === l.line) {
          last.endLine = l.line + 1;
        } else {
          chunks.push({ paragraph: l.para, startLine: l.line, endLine: l.line + 1 });
        }
      }
      pages.push({ chunks, usedHeight, overflow: false });
    }
  }

  return { pages, pageCount: pages.length, violations, remainingSquareSum };
}
