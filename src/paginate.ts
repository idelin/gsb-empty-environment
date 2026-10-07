/**
 * 动态规划分页：在合法分页中先最小化页数，再最小化各页剩余高度平方和。
 *
 * 状态 f[g] = 放置 lines[g..L) 的最优 (页数, 代价)，转移枚举当前页 lines[g, e)。
 * 若严格的分裂约束（页尾页首各至少 2 行）下无解，则放宽为 1 行重试
 * （此时每行都可独占一页，必然有解）。
 */
import {
  EPS,
  MIN_SPLIT_LINES,
  buildPagination,
  flattenDocument,
  isValidPage,
  remainingCost,
  type FlatDocument,
  type Pagination,
} from './rules.ts';

export type { Page, PageSegment, Pagination } from './rules.ts';

export function paginate(paragraphs: number[][], pageHeight: number): Pagination {
  const doc = flattenDocument(paragraphs, pageHeight);
  const solved = solve(doc, pageHeight, MIN_SPLIT_LINES) ?? solve(doc, pageHeight, 1);
  // minSplitLines = 1 时每行都能独占一页（单行超页允许溢出），必有解
  return solved!;
}

function solve(doc: FlatDocument, pageHeight: number, minSplitLines: number): Pagination | null {
  const L = doc.lines.length;
  if (L === 0) return { pages: [], pageCount: 0, remainingSquareSum: 0 };

  const bestPages = new Array<number>(L + 1).fill(Infinity);
  const bestCost = new Array<number>(L + 1).fill(Infinity);
  const next = new Array<number>(L + 1).fill(-1);
  bestPages[L] = 0;
  bestCost[L] = 0;

  for (let g = L - 1; g >= 0; g--) {
    const paraEnd = doc.paraEnd[doc.lines[g].paragraphIndex];
    let sum = 0;
    for (let e = g + 1; e <= L; e++) {
      sum += doc.lines[e - 1].height;
      // 超出页高且已越过本段末尾：再加行只会更高，且不可能构成整段溢出页
      if (sum > pageHeight + EPS && e > paraEnd) break;
      if (!isValidPage(doc, g, e, sum, pageHeight, minSplitLines)) continue;
      if (bestPages[e] === Infinity) continue;
      const candPages = bestPages[e] + 1;
      const candCost = bestCost[e] + remainingCost(sum, pageHeight);
      const better =
        candPages < bestPages[g] ||
        (candPages === bestPages[g] && candCost < bestCost[g] - EPS) ||
        // 平局时优先把前面的页填满（减少不必要的段落分裂）
        (candPages === bestPages[g] && Math.abs(candCost - bestCost[g]) <= EPS && e > next[g]);
      if (better) {
        bestPages[g] = candPages;
        bestCost[g] = candCost;
        next[g] = e;
      }
    }
  }

  if (next[0] === -1) return null;
  const pageEnds: number[] = [];
  for (let cur = 0; cur < L; cur = next[cur]) pageEnds.push(next[cur]);
  return buildPagination(doc, pageHeight, pageEnds);
}
