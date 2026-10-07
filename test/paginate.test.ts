import test from 'node:test';
import assert from 'node:assert/strict';
import { paginate, paginateBruteForce, type Pagination } from '../src/index.ts';
import { EPS, flattenDocument, isValidPage, MIN_SPLIT_LINES } from '../src/rules.ts';

/** 校验分页结果结构：顺序还原原文档、逐页合法 */
function assertWellFormed(paragraphs: number[][], pageHeight: number, result: Pagination): void {
  const doc = flattenDocument(paragraphs, pageHeight);
  // 段不重不漏、顺序还原全部行
  const rebuilt: number[][] = paragraphs.map(() => []);
  for (const page of result.pages) {
    for (const seg of page.segments) {
      for (let i = seg.startLine; i < seg.endLine; i++) {
        rebuilt[seg.paragraphIndex].push(paragraphs[seg.paragraphIndex][i]);
      }
    }
  }
  assert.deepEqual(rebuilt, paragraphs, '分页后按顺序应还原原文档');

  // 逐页用独立规则校验合法性，并核对 usedHeight / overflow
  let g = 0;
  for (const page of result.pages) {
    const lineCount = page.segments.reduce((n, s) => n + (s.endLine - s.startLine), 0);
    const e = g + lineCount;
    assert.ok(
      isValidPage(doc, g, e, page.usedHeight, pageHeight, 1) ||
        isValidPage(doc, g, e, page.usedHeight, pageHeight, MIN_SPLIT_LINES),
      `页 [${g}, ${e}) 不合法`,
    );
    assert.equal(page.overflow, page.usedHeight > pageHeight + EPS);
    g = e;
  }
  assert.equal(g, doc.lines.length);
  assert.equal(result.pageCount, result.pages.length);
}

test('空文档得到 0 页', () => {
  const r = paginate([], 100);
  assert.equal(r.pageCount, 0);
  assert.equal(r.remainingSquareSum, 0);
});

test('空段落被跳过且保留原始下标', () => {
  const r = paginate([[], [2, 2], []], 10);
  assert.equal(r.pageCount, 1);
  assert.deepEqual(r.pages[0].segments, [{ paragraphIndex: 1, startLine: 0, endLine: 2 }]);
});

test('全部放得下时只有一页', () => {
  const r = paginate([[1, 2], [3]], 10);
  assert.equal(r.pageCount, 1);
  assert.equal(r.pages[0].usedHeight, 6);
  assert.equal(r.remainingSquareSum, 16);
});

test('保持顺序且页高固定', () => {
  const r = paginate([[3], [3], [3], [3]], 6);
  assert.equal(r.pageCount, 2);
  for (const p of r.pages) assert.ok(p.usedHeight <= 6 + EPS);
  assertWellFormed([[3], [3], [3], [3]], 6, r);
});

test('段落分裂时页尾页首至少各两行', () => {
  const paragraphs = [[3, 3, 3, 3, 3]];
  const r = paginate(paragraphs, 10);
  // 合法断点只有 2|3 和 3|2；代价相同，平局时优先填满前页 -> 3|2
  assert.equal(r.pageCount, 2);
  assert.deepEqual(r.pages[0].segments, [{ paragraphIndex: 0, startLine: 0, endLine: 3 }]);
  assert.deepEqual(r.pages[1].segments, [{ paragraphIndex: 0, startLine: 3, endLine: 5 }]);
  assertWellFormed(paragraphs, 10, r);
});

test('整段超页可分：长段跨页且每块至少两行', () => {
  const paragraphs = [[4, 4, 4, 4, 4, 4]];
  const r = paginate(paragraphs, 10);
  assert.equal(r.pageCount, 3);
  for (const p of r.pages) {
    assert.deepEqual(p.segments[0].endLine! - p.segments[0].startLine!, 2);
  }
  assertWellFormed(paragraphs, 10, r);
});

test('单行超页单列溢出：超高行独占一页', () => {
  const paragraphs = [[2, 2, 8, 2, 2]];
  const r = paginate(paragraphs, 5);
  assert.equal(r.pageCount, 3);
  assert.deepEqual(r.pages[1].segments, [{ paragraphIndex: 0, startLine: 2, endLine: 3 }]);
  assert.equal(r.pages[1].overflow, true);
  assert.equal(r.pages[0].overflow, false);
  assertWellFormed(paragraphs, 5, r);
});

test('单行段落超页：独占一页并溢出', () => {
  const r = paginate([[9]], 5);
  assert.equal(r.pageCount, 1);
  assert.equal(r.pages[0].overflow, true);
});

test('不可分短段整段超页：整段独占一页溢出', () => {
  const paragraphs = [[4, 4, 4]]; // 3 行 < 4，无合法分裂点
  const r = paginate(paragraphs, 5);
  assert.equal(r.pageCount, 1);
  assert.equal(r.pages[0].usedHeight, 12);
  assert.equal(r.pages[0].overflow, true);
  assertWellFormed(paragraphs, 5, r);
});

test('严格分裂无解时放宽到单行分裂（级联兜底）', () => {
  const paragraphs = [[4, 4, 4, 4]]; // 两行块高 8 > 5，严格规则无解
  const r = paginate(paragraphs, 5);
  assert.equal(r.pageCount, 4);
  for (const p of r.pages) assert.equal(p.overflow, false);
  assertWellFormed(paragraphs, 5, r);
});

test('同页数时选剩余高度平方和更小者', () => {
  // [8][2,2] 代价 4+36=40 优于 [8,2][2] 代价 0+64=64
  const r = paginate([[8], [2], [2]], 10);
  assert.equal(r.pageCount, 2);
  assert.equal(r.remainingSquareSum, 40);
  assert.equal(r.pages[0].usedHeight, 8);
  assert.equal(r.pages[1].usedHeight, 4);
});

test('先最少页再最小代价', () => {
  // 2 页必为 (10,5) 代价 25；3 页 (5,5,5) 代价 75 —— 页数优先
  const r = paginate([[5], [5], [5]], 10);
  assert.equal(r.pageCount, 2);
  assert.equal(r.remainingSquareSum, 25);
});

test('非法输入抛错', () => {
  assert.throws(() => paginate([[1]], 0), RangeError);
  assert.throws(() => paginate([[1]], -3), RangeError);
  assert.throws(() => paginate([[1, -1]], 10), RangeError);
  assert.throws(() => paginate([[NaN]], 10), RangeError);
});

// ---------- 短文档断点穷举交叉核对 ----------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomDocument(rand: () => number): number[][] {
  const paraCount = 1 + Math.floor(rand() * 4);
  const paragraphs: number[][] = [];
  let lines = 0;
  for (let p = 0; p < paraCount; p++) {
    const n = 1 + Math.floor(rand() * 6);
    const para: number[] = [];
    for (let i = 0; i < n; i++) {
      // 大多数行普通高度，少数行超高以触发溢出 / 级联
      const h = rand() < 0.12 ? 8 + Math.floor(rand() * 18) : 1 + Math.floor(rand() * 8);
      para.push(rand() < 0.3 ? h + 0.5 : h); // 部分小数高度
    }
    lines += n;
    if (lines > 14) break; // 控制穷举规模
    paragraphs.push(para);
  }
  return paragraphs;
}

test('短文档：DP 与断点穷举结果一致', () => {
  const rand = mulberry32(20261007);
  const cases = 400;
  for (let k = 0; k < cases; k++) {
    const paragraphs = randomDocument(rand);
    const pageHeight = 6 + Math.floor(rand() * 15);
    const dp = paginate(paragraphs, pageHeight);
    const bf = paginateBruteForce(paragraphs, pageHeight);
    assert.equal(
      dp.pageCount,
      bf.pageCount,
      `用例 ${k} 页数不一致: ${JSON.stringify(paragraphs)} H=${pageHeight}`,
    );
    assert.ok(
      Math.abs(dp.remainingSquareSum - bf.remainingSquareSum) < 1e-6,
      `用例 ${k} 代价不一致: dp=${dp.remainingSquareSum} bf=${bf.remainingSquareSum} ` +
        `${JSON.stringify(paragraphs)} H=${pageHeight}`,
    );
    assertWellFormed(paragraphs, pageHeight, dp);
  }
});
