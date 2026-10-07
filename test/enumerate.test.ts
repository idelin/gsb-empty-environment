import { test } from "node:test";
import assert from "node:assert/strict";
import {
  paginate,
  table,
  line,
  group,
  enumeratePartitions,
  verifyWithEnumeration,
} from "../src/index.ts";
import type { BlockSpec } from "../src/index.ts";

test("enumeration confirms greedy page count on a footnote reflow case", () => {
  const spec = table(0, [line(10, "a"), line(10, "b"), line(10, "c")]);
  const report = verifyWithEnumeration(spec, { pageHeight: 30, footnoteHeight: 10 });
  assert.equal(report.greedyPages, 2);
  assert.equal(report.minPages, 2);
  assert.ok(report.greedyIsOptimal);
});

test("enumeration sees the footnote constraint on the last page only", () => {
  // 6 rows of 10, capacity 30, footnote 10 -> last page holds at most 2 rows,
  // so 3 pages is the optimum; without a footnote it would be 2.
  const spec = table(0, [line(10), line(10), line(10), line(10), line(10), line(10)]);
  const withNote = enumeratePartitions(spec, { pageHeight: 30, footnoteHeight: 10 });
  const without = enumeratePartitions(spec, { pageHeight: 30 });
  assert.equal(withNote.minPages, 3);
  assert.equal(without.minPages, 2);
});

test("enumeration respects unbreakable groups", () => {
  const spec = table(0, [group("g", [6, 6]), line(8), line(4)]);
  // capacity 12: group (12) fills a page alone; 8 + 4 share the next.
  const { minPages } = enumeratePartitions(spec, { pageHeight: 12 });
  assert.equal(minPages, 2);
});

test("verifyWithEnumeration rejects a doctored result", () => {
  const spec = table(0, [line(5, "a"), line(5, "b")]);
  const result = paginate(spec, { pageHeight: 10 });
  result.pages[0].contentHeight = 99;
  assert.throws(() => verifyWithEnumeration(spec, { pageHeight: 10 }, result), /exceeds/);
});

// Seeded PRNG for reproducible randomized cross-checks.
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

test("greedy matches exhaustive enumeration on randomized small inputs", () => {
  const rand = mulberry32(20261008);
  const int = (lo: number, hi: number) => lo + Math.floor(rand() * (hi - lo + 1));

  const cases = 300;
  for (let c = 0; c < cases; c++) {
    const headerHeight = int(0, 8);
    const capacity = headerHeight + int(6, 30);
    const pageHeight = capacity; // capacity = pageHeight - headerHeight
    const footnoteHeight = int(0, Math.max(0, capacity - headerHeight - 4));

    const blockCount = int(1, 12);
    const blocks: BlockSpec[] = [];
    for (let i = 0; i < blockCount; i++) {
      if (rand() < 0.35) {
        // merged-cell group of 2-4 rows, kept within capacity
        const rowCount = int(2, 4);
        const maxRow = Math.max(1, Math.floor((capacity - headerHeight) / rowCount));
        blocks.push(
          group(`g${c}-${i}`, Array.from({ length: rowCount }, () => int(1, maxRow))),
        );
      } else {
        blocks.push(line(int(1, Math.max(1, capacity - headerHeight)), `l${c}-${i}`));
      }
    }
    const spec = table(headerHeight, blocks);
    const options = { pageHeight, footnoteHeight };
    const report = verifyWithEnumeration(spec, options);
    assert.ok(
      report.greedyIsOptimal,
      `case ${c}: greedy ${report.greedyPages} != optimal ${report.minPages} ` +
        `(pageHeight=${pageHeight}, header=${headerHeight}, footnote=${footnoteHeight}, ` +
        `blocks=${JSON.stringify(blocks.map((b) => b.rows.map((r) => r.height)))})`,
    );
  }
});

test("randomized last-page reflow never strands the footnote", () => {
  const rand = mulberry32(42);
  const int = (lo: number, hi: number) => lo + Math.floor(rand() * (hi - lo + 1));
  for (let c = 0; c < 200; c++) {
    const headerHeight = int(0, 6);
    const pageHeight = headerHeight + int(8, 24);
    const footnoteHeight = int(1, pageHeight - headerHeight - 1);
    const blocks: BlockSpec[] = Array.from({ length: int(1, 10) }, (_, i) =>
      line(int(1, pageHeight - headerHeight), `b${i}`),
    );
    const result = paginate(table(headerHeight, blocks), { pageHeight, footnoteHeight });
    const last = result.pages[result.pages.length - 1];
    assert.equal(last.footnoteHeight, footnoteHeight);
    assert.ok(last.usedHeight <= pageHeight, `case ${c}: last page overfull`);
    for (const page of result.pages.slice(0, -1)) {
      assert.equal(page.footnoteHeight, 0);
      assert.ok(page.usedHeight <= pageHeight, `case ${c}: page ${page.index} overfull`);
    }
  }
});
