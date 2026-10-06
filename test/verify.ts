/**
 * Verifies src/paginate.ts:
 *  1. structural invariants of every result (order, coverage, heights);
 *  2. hand-picked cases for each documented behaviour;
 *  3. randomized short documents cross-checked against exhaustive
 *     breakpoint enumeration (test/brute.ts).
 *
 * Run with Node >= 23.6 (native type stripping):  node test/verify.ts
 */

import { paginate, type PaginateResult } from "../src/paginate.ts";
import { bruteForce } from "./brute.ts";

let failures = 0;

function check(cond: boolean, msg: string): void {
  if (!cond) {
    failures++;
    console.error(`FAIL: ${msg}`);
  }
}

function approxEq(a: number, b: number, eps = 1e-9): boolean {
  return Math.abs(a - b) <= eps * Math.max(1, Math.abs(a), Math.abs(b));
}

/** Re-derive every reported statistic from the pages themselves. */
function validateStructure(
  doc: number[][],
  pageHeight: number,
  result: PaginateResult,
  label: string,
): void {
  // 1. Pages reproduce the exact line sequence, in order.
  const flat: Array<[number, number]> = [];
  doc.forEach((lines, para) => lines.forEach((_, line) => flat.push([para, line])));
  const got: Array<[number, number]> = [];
  for (const page of result.pages) {
    for (const chunk of page.chunks) {
      for (let line = chunk.startLine; line < chunk.endLine; line++) {
        got.push([chunk.paragraph, line]);
      }
    }
  }
  check(
    JSON.stringify(flat) === JSON.stringify(got),
    `${label}: pages do not reproduce the document in order`,
  );
  check(result.pageCount === result.pages.length, `${label}: pageCount mismatch`);

  // 2. Per-page invariants + recomputed objective.
  let violations = 0;
  let ssq = 0;
  for (const page of result.pages) {
    let used = 0;
    for (const chunk of page.chunks) {
      for (let line = chunk.startLine; line < chunk.endLine; line++) {
        used += doc[chunk.paragraph][line];
      }
    }
    check(approxEq(used, page.usedHeight), `${label}: usedHeight wrong on a page`);
    const rem = pageHeight - page.usedHeight;
    ssq += rem * rem;
    if (page.overflow) {
      const one = page.chunks.length === 1 && page.chunks[0].endLine - page.chunks[0].startLine === 1;
      check(one, `${label}: overflow page must hold exactly one line`);
      check(page.usedHeight > pageHeight, `${label}: overflow page without oversized line`);
      continue;
    }
    check(page.usedHeight <= pageHeight, `${label}: non-overflow page exceeds page height`);
    // Two-line rule, recomputed from the document: a chunk is a split
    // fragment when its paragraph continues on another page across normal
    // lines (an adjacent oversized line is a hard break, not a split).
    // A page counts at most one violation, matching the library.
    let pageViolates = false;
    for (const chunk of page.chunks) {
      const para = doc[chunk.paragraph];
      const continuesUp =
        chunk.startLine > 0 && para[chunk.startLine - 1] <= pageHeight;
      const continuesDown =
        chunk.endLine < para.length && para[chunk.endLine] <= pageHeight;
      if ((continuesUp || continuesDown) && chunk.endLine - chunk.startLine < 2) {
        pageViolates = true;
      }
    }
    if (pageViolates) violations++;
  }
  check(result.violations === violations, `${label}: violations ${result.violations} != recomputed ${violations}`);
  check(approxEq(result.remainingSquareSum, ssq), `${label}: remainingSquareSum mismatch`);
}

function expectCost(
  doc: number[][],
  pageHeight: number,
  want: { pages: number; violations?: number; ssq?: number },
  label: string,
): PaginateResult {
  const result = paginate(doc, pageHeight);
  validateStructure(doc, pageHeight, result, label);
  check(result.pageCount === want.pages, `${label}: pages ${result.pageCount}, want ${want.pages}`);
  if (want.violations !== undefined) {
    check(result.violations === want.violations, `${label}: violations ${result.violations}, want ${want.violations}`);
  }
  if (want.ssq !== undefined) {
    check(approxEq(result.remainingSquareSum, want.ssq), `${label}: ssq ${result.remainingSquareSum}, want ${want.ssq}`);
  }
  return result;
}

// ---------- fixed cases ----------

// Empty and trivial inputs.
expectCost([], 10, { pages: 0, violations: 0, ssq: 0 }, "empty doc");
expectCost([[]], 10, { pages: 0, violations: 0, ssq: 0 }, "empty paragraph");
expectCost([[4]], 10, { pages: 1, violations: 0, ssq: 36 }, "single line");

// Exact fit, order preserved.
expectCost([[5, 5], [5, 5]], 10, { pages: 2, violations: 0, ssq: 0 }, "exact fit");

// Two-line rule: a 4-line paragraph splits 2+2, never 3+1 or 1+3.
{
  const r = expectCost([[4, 4, 4, 4]], 10, { pages: 2, violations: 0, ssq: 8 }, "split 2+2");
  const frag = r.pages.map((p) => p.chunks[0].endLine - p.chunks[0].startLine);
  check(JSON.stringify(frag) === "[2,2]", `split 2+2: got fragments ${frag}`);
}

// Paragraph taller than a page splits; every fragment keeps >= 2 lines.
// 10 lines of height 3, page fits 3 lines: [3,3,3,1] would violate, so the
// optimum uses fragment sizes 2/3 only, e.g. remainders 4,4,1,1 -> ssq 34.
{
  const doc = [new Array<number>(10).fill(3)];
  const r = expectCost(doc, 10, { pages: 4, violations: 0, ssq: 34 }, "paragraph over page");
  const frag = r.pages.map((p) => p.chunks[0].endLine - p.chunks[0].startLine);
  check(Math.min(...frag) >= 2, `paragraph over page: fragment under 2 in ${frag}`);
}

// Single oversized line gets its own overflowing page and splits its
// paragraph into independent fragments.
{
  const r = expectCost([[3, 25, 3]], 10, { pages: 3, violations: 0 }, "oversized middle line");
  check(r.pages[1].overflow && r.pages[1].usedHeight === 25, "oversized middle line: overflow page");
  check(!r.pages[0].overflow && !r.pages[2].overflow, "oversized middle line: side pages normal");
  const seq = r.pages.map((p) => `${p.chunks[0].startLine}-${p.chunks[0].endLine}`);
  check(JSON.stringify(seq) === '["0-1","1-2","2-3"]', `oversized middle line: chunks ${seq}`);
}

// Oversized line at the very start/end of the document.
expectCost([[20, 4, 4]], 10, { pages: 2, violations: 0 }, "oversized first line");
expectCost([[4, 4, 20]], 10, { pages: 2, violations: 0 }, "oversized last line");

// Unsatisfiable two-line rule: page fits only one line of a 3-line
// paragraph at a time, so every page is a single-line split fragment.
expectCost([[6, 6, 6]], 10, { pages: 3, violations: 3 }, "degenerate paragraph");

// Min pages first: [7],[2],[8] fits as 2 pages and must not be "balanced"
// into 3; among the 2-page layouts, remainders (1,2) beat (3,0).
{
  const r = expectCost([[7], [2], [8]], 10, { pages: 2, violations: 0, ssq: 5 }, "min pages first");
  check(r.pages[0].chunks.length === 2, "min pages first: first page packs 7+2");
}

// Then min sum of squared remainders: remainders (1,7) beat (0,8).
{
  const r = expectCost([[9], [1], [1], [1]], 10, { pages: 2, violations: 0, ssq: 50 }, "min ssq");
  check(r.pages[0].chunks.length === 1 && r.pages[1].chunks.length === 3, "min ssq: balanced fill");
}

// Zero-height lines are allowed and pack together.
expectCost([[0, 0, 0], [0]], 10, { pages: 1, violations: 0, ssq: 100 }, "zero heights");

// Fractional heights.
expectCost([[2.5, 2.5, 3.5], [4.5]], 10, { pages: 2, violations: 0, ssq: 32.5 }, "fractional");

// ---------- randomized cross-check against brute force ----------

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

function randomSweep(trials: number, seedBase: number, minPage: number, spanPage: number): number {
  let ran = 0;
  for (let trial = 0; trial < trials; trial++) {
  const rand = mulberry32(seedBase + trial + 1);
  const pageHeight = minPage + Math.floor(rand() * spanPage);
  const paraCount = 1 + Math.floor(rand() * 4);
  const doc: number[][] = [];
  let total = 0;
  for (let p = 0; p < paraCount; p++) {
    const lines: number[] = [];
    const lineCount = 1 + Math.floor(rand() * 5);
    for (let k = 0; k < lineCount && total < 13; k++) {
      total++;
      if (rand() < 0.08) lines.push(pageHeight + 1 + Math.floor(rand() * 5)); // oversized
      else lines.push(1 + Math.floor(rand() * Math.min(9, pageHeight)));
    }
    lines.length > 0 && doc.push(lines);
  }
  const result = paginate(doc, pageHeight);
  validateStructure(doc, pageHeight, result, `random#${seedBase + trial}`);
  const brute = bruteForce(doc, pageHeight);
  const same =
    result.violations === brute.violations &&
    result.pageCount === brute.pageCount &&
    approxEq(result.remainingSquareSum, brute.remainingSquareSum);
  if (!same) {
    failures++;
    console.error(
      `FAIL random#${seedBase + trial}: doc=${JSON.stringify(doc)} H=${pageHeight}\n` +
        `  dp:    v=${result.violations} p=${result.pageCount} ssq=${result.remainingSquareSum}\n` +
        `  brute: v=${brute.violations} p=${brute.pageCount} ssq=${brute.remainingSquareSum}`,
    );
  }
  ran++;
  }
  return ran;
}

// Roomy pages: splits common, violations rare.
let trials = randomSweep(600, 0, 8, 7);
// Tight pages: two lines often cannot share a page, forcing violations.
trials += randomSweep(600, 100000, 4, 4);

if (failures > 0) {
  console.error(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log(`all checks passed (fixed cases + ${trials} randomized docs vs brute force)`);
