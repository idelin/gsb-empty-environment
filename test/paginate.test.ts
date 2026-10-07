import { test } from "node:test";
import assert from "node:assert/strict";
import {
  paginate,
  table,
  line,
  group,
  PaginationOverflowError,
} from "../src/index.ts";

test("paginates rows by line height across pages", () => {
  const spec = table(10, [line(8, "a"), line(8, "b"), line(8, "c"), line(8, "d")]);
  // capacity = 30 - 10 = 20 -> two 8-unit rows per page
  const result = paginate(spec, { pageHeight: 30 });
  assert.equal(result.pageCount, 2);
  assert.deepEqual(
    result.pages.map((p) => p.blocks.map((b) => b.id)),
    [["a", "b"], ["c", "d"]],
  );
});

test("header repeats on every page and consumes page height", () => {
  const spec = table(10, [line(15, "a"), line(15, "b")]);
  const result = paginate(spec, { pageHeight: 30 });
  // capacity 20 fits only one 15-unit row per page
  assert.equal(result.pageCount, 2);
  for (const page of result.pages) {
    assert.equal(page.headerHeight, 10);
    assert.equal(page.blocks[0].offsetY, 10, "first block starts below the header");
    assert.ok(page.usedHeight <= 30);
  }
});

test("merged-cell row group is unbreakable and moves as a whole", () => {
  const spec = table(0, [
    line(6, "top"),
    group("merged", [5, 5, 5]), // 15 units, atomic
    line(4, "tail"),
  ]);
  // capacity 20: "top" (6) + group (15) = 21 > 20, so the whole group
  // must move to page 2 rather than straddling the boundary.
  const result = paginate(spec, { pageHeight: 20 });
  assert.equal(result.pageCount, 2);
  assert.deepEqual(
    result.pages.map((p) => p.blocks.map((b) => b.id)),
    [["top"], ["merged", "tail"]],
  );
  const merged = result.pages[1].blocks[0];
  assert.equal(merged.rows.length, 3);
  assert.deepEqual(
    merged.rows.map((r) => r.offsetY),
    [0, 5, 10],
    "group rows stay contiguous on one page",
  );
});

test("oversized group throws naming the group instead of looping forever", () => {
  const spec = table(10, [
    line(5, "fine"),
    group("huge-merged", [20, 20, 20]),
    line(5, "after"),
  ]);
  // capacity = 40 - 10 = 30 < 60 required by the group
  let err: PaginationOverflowError | undefined;
  try {
    paginate(spec, { pageHeight: 40 });
  } catch (e) {
    err = e as PaginationOverflowError;
  }
  assert.ok(err instanceof PaginationOverflowError);
  assert.equal(err.blockId, "huge-merged");
  assert.equal(err.blockIndex, 1);
  assert.equal(err.required, 60);
  assert.equal(err.available, 30);
  assert.match(err.message, /huge-merged/);
});

test("a group exactly filling the capacity still paginates", () => {
  const spec = table(10, [group("exact", [15, 15]), line(10, "next")]);
  const result = paginate(spec, { pageHeight: 40 }); // capacity 30
  assert.equal(result.pageCount, 2);
  assert.deepEqual(result.pages[0].blocks.map((b) => b.id), ["exact"]);
});

test("footnote occupies height on the last page only", () => {
  const spec = table(10, [line(8, "a"), line(8, "b"), line(8, "c")]);
  // capacity 20; last page capacity 20 - 6 = 14
  const result = paginate(spec, { pageHeight: 30, footnoteHeight: 6 });
  assert.equal(result.pageCount, 2);
  assert.equal(result.pages[0].footnoteHeight, 0);
  assert.equal(result.pages[1].footnoteHeight, 6);
  assert.equal(result.pages[1].usedHeight, 10 + 8 + 6);
});

test("footnote reflows the tail block onto a new last page", () => {
  const spec = table(0, [line(10, "a"), line(10, "b"), line(10, "c")]);
  // capacity 30 fits exactly 3 rows with no footnote...
  assert.equal(paginate(spec, { pageHeight: 30 }).pageCount, 1);
  // ...but reserving 10 for the footnote forces a second page. The last page
  // takes the longest suffix that fits beside the footnote.
  const result = paginate(spec, { pageHeight: 30, footnoteHeight: 10 });
  assert.equal(result.pageCount, 2);
  assert.deepEqual(
    result.pages.map((p) => p.blocks.map((b) => b.id)),
    [["a"], ["b", "c"]],
  );
  assert.equal(result.pages[1].footnoteHeight, 10);
  assert.ok(result.pages[1].usedHeight <= 30);
});

test("footnote-only final page when the last block cannot share with it", () => {
  const spec = table(0, [line(10, "a"), line(18, "big")]);
  // capacity 20, footnote 5 -> last page content capacity 15 < 18.
  const result = paginate(spec, { pageHeight: 20, footnoteHeight: 5 });
  assert.equal(result.pageCount, 3);
  assert.deepEqual(
    result.pages.map((p) => p.blocks.map((b) => b.id)),
    [["a"], ["big"], []],
  );
  assert.equal(result.pages[2].footnoteHeight, 5);
  assert.equal(result.pages[2].contentHeight, 0);
});

test("footnote larger than capacity is rejected up front", () => {
  const spec = table(10, [line(5, "a")]);
  assert.throws(() => paginate(spec, { pageHeight: 30, footnoteHeight: 25 }), /footnoteHeight/);
});

test("header taller than the page is rejected up front", () => {
  const spec = table(30, [line(5, "a")]);
  assert.throws(() => paginate(spec, { pageHeight: 30 }), /headerHeight/);
});

test("empty table yields a single page carrying header and footnote", () => {
  const result = paginate(table(10, []), { pageHeight: 30, footnoteHeight: 4 });
  assert.equal(result.pageCount, 1);
  assert.equal(result.pages[0].usedHeight, 14);
});
