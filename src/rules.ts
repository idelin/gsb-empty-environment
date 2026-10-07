/**
 * 分页规则共享模块：DP 主算法与断点穷举核对实现都基于这里的判定函数，
 * 保证两者对"什么是合法分页"的理解完全一致。
 */

/** 浮点比较容差 */
export const EPS = 1e-9;

/** 段落分裂时，页尾 / 页首各自至少保留的行数 */
export const MIN_SPLIT_LINES = 2;

/** 拍平后的行：记录它属于哪个原始段落、段内第几行 */
export interface FlatLine {
  paragraphIndex: number;
  lineIndex: number;
  height: number;
}

/** 拍平后的文档 */
export interface FlatDocument {
  lines: FlatLine[];
  /** 按原始段落下标索引：该段第一行在 lines 中的位置（空段等于 paraEnd） */
  paraStart: number[];
  /** 按原始段落下标索引：该段最后一行之后的位置 */
  paraEnd: number[];
  /** 按原始段落下标索引：段落行数 */
  paraLen: number[];
}

/** 一页中的一个段落片段（行区间半开 [startLine, endLine)） */
export interface PageSegment {
  paragraphIndex: number;
  startLine: number;
  endLine: number;
}

export interface Page {
  segments: PageSegment[];
  usedHeight: number;
  /** 内容高度超过页高（单行超页或不可分短段整段超页） */
  overflow: boolean;
}

export interface Pagination {
  pages: Page[];
  /** 第一目标：总页数 */
  pageCount: number;
  /** 第二目标：各页剩余高度（max(0, 页高 - 已用高度)）的平方和 */
  remainingSquareSum: number;
}

/** 校验输入并拍平文档。空段落（0 行）被跳过，但保留原始段落下标。 */
export function flattenDocument(paragraphs: number[][], pageHeight: number): FlatDocument {
  if (!Number.isFinite(pageHeight) || pageHeight <= 0) {
    throw new RangeError(`pageHeight 必须为正数，收到 ${pageHeight}`);
  }
  const lines: FlatLine[] = [];
  const paraStart: number[] = [];
  const paraEnd: number[] = [];
  const paraLen: number[] = [];
  for (let p = 0; p < paragraphs.length; p++) {
    paraStart.push(lines.length);
    const heights = paragraphs[p];
    for (let i = 0; i < heights.length; i++) {
      const h = heights[i];
      if (!Number.isFinite(h) || h < 0) {
        throw new RangeError(`段落 ${p} 第 ${i} 行高度非法：${h}`);
      }
      lines.push({ paragraphIndex: p, lineIndex: i, height: h });
    }
    paraEnd.push(lines.length);
    paraLen.push(heights.length);
  }
  return { lines, paraStart, paraEnd, paraLen };
}

/** 段落是否存在合法分裂点（两侧都至少 minSplitLines 行） */
export function hasSplitPoint(paragraphLength: number, minSplitLines: number): boolean {
  return paragraphLength >= 2 * minSplitLines;
}

/** 在 lines[boundary-1] 与 lines[boundary] 之间是否允许分页 */
export function isBreakAllowed(doc: FlatDocument, boundary: number, minSplitLines: number): boolean {
  const before = doc.lines[boundary - 1];
  const after = doc.lines[boundary];
  if (before.paragraphIndex !== after.paragraphIndex) return true;
  const t = after.lineIndex;
  const len = doc.paraLen[after.paragraphIndex];
  return t >= minSplitLines && len - t >= minSplitLines;
}

/**
 * 判定 lines[g, e) 作为一页是否合法：
 * 1. 页首、页尾处的断点必须满足段落分裂约束（页尾页首各至少 minSplitLines 行）；
 * 2. 总高不超过页高；以下两种溢出例外：
 *    a. 页内只有一行 —— 单行超页，单列溢出；
 *    b. 页内恰好是一个完整段落且该段不存在合法分裂点 —— 整段超页，整段溢出。
 */
export function isValidPage(
  doc: FlatDocument,
  g: number,
  e: number,
  sum: number,
  pageHeight: number,
  minSplitLines: number,
): boolean {
  const total = doc.lines.length;
  if (g > 0 && !isBreakAllowed(doc, g, minSplitLines)) return false;
  if (e < total && !isBreakAllowed(doc, e, minSplitLines)) return false;
  if (sum <= pageHeight + EPS) return true;
  if (e - g === 1) return true;
  const p = doc.lines[g].paragraphIndex;
  return (
    doc.paraStart[p] === g &&
    doc.paraEnd[p] === e &&
    !hasSplitPoint(doc.paraLen[p], minSplitLines)
  );
}

/** 单页代价：剩余高度（不足 0 按 0 计）的平方 */
export function remainingCost(usedHeight: number, pageHeight: number): number {
  const r = pageHeight - usedHeight;
  return r > 0 ? r * r : 0;
}

/** 由页结束位置序列（递增、最后一个为总行数）构建分页结果 */
export function buildPagination(doc: FlatDocument, pageHeight: number, pageEnds: number[]): Pagination {
  const pages: Page[] = [];
  let g = 0;
  let remainingSquareSum = 0;
  for (const e of pageEnds) {
    let usedHeight = 0;
    const segments: PageSegment[] = [];
    for (let i = g; i < e; i++) {
      const line = doc.lines[i];
      usedHeight += line.height;
      const last = segments[segments.length - 1];
      if (last && last.paragraphIndex === line.paragraphIndex) {
        last.endLine = line.lineIndex + 1;
      } else {
        segments.push({
          paragraphIndex: line.paragraphIndex,
          startLine: line.lineIndex,
          endLine: line.lineIndex + 1,
        });
      }
    }
    remainingSquareSum += remainingCost(usedHeight, pageHeight);
    pages.push({ segments, usedHeight, overflow: usedHeight > pageHeight + EPS });
    g = e;
  }
  return { pages, pageCount: pages.length, remainingSquareSum };
}
