/**
 * 断点穷举分页：枚举 L-1 个行间断点的全部子集，逐一校验合法性并取最优。
 * 与 DP 实现完全独立，用于短文档上交叉核对 DP 的最优性。
 */
import {
  EPS,
  MIN_SPLIT_LINES,
  buildPagination,
  flattenDocument,
  isValidPage,
  type FlatDocument,
  type Pagination,
} from './rules.ts';

export type { Pagination } from './rules.ts';

/** 穷举允许的最大行数（L-1 个断点 -> 2^(L-1) 种子集） */
export const BRUTE_FORCE_MAX_LINES = 24;

export function paginateBruteForce(paragraphs: number[][], pageHeight: number): Pagination {
  const doc = flattenDocument(paragraphs, pageHeight);
  const solved = enumerate(doc, pageHeight, MIN_SPLIT_LINES) ?? enumerate(doc, pageHeight, 1);
  return solved!;
}

function enumerate(doc: FlatDocument, pageHeight: number, minSplitLines: number): Pagination | null {
  const L = doc.lines.length;
  if (L === 0) return { pages: [], pageCount: 0, remainingSquareSum: 0 };
  if (L > BRUTE_FORCE_MAX_LINES) {
    throw new Error(`文档 ${L} 行，超过穷举上限 ${BRUTE_FORCE_MAX_LINES} 行`);
  }

  let best: Pagination | null = null;
  const subsets = 1 << (L - 1);
  for (let mask = 0; mask < subsets; mask++) {
    // 按掩码切页并逐页校验
    const pageEnds: number[] = [];
    let g = 0;
    let sum = 0;
    let valid = true;
    for (let i = 0; i < L; i++) {
      sum += doc.lines[i].height;
      const isPageEnd = i === L - 1 || ((mask >> i) & 1) === 1;
      if (!isPageEnd) continue;
      if (!isValidPage(doc, g, i + 1, sum, pageHeight, minSplitLines)) {
        valid = false;
        break;
      }
      pageEnds.push(i + 1);
      g = i + 1;
      sum = 0;
    }
    if (!valid) continue;

    const candidate = buildPagination(doc, pageHeight, pageEnds);
    if (
      best === null ||
      candidate.pageCount < best.pageCount ||
      (candidate.pageCount === best.pageCount &&
        candidate.remainingSquareSum < best.remainingSquareSum - EPS)
    ) {
      best = candidate;
    }
  }
  return best;
}
