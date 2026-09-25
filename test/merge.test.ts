import { test } from "node:test";
import assert from "node:assert/strict";
import { MergeMap } from "../src/merge.ts";
import { fixtureGrid } from "../src/fixtures/nodeTree.ts";

const merges = new MergeMap(fixtureGrid.merges);

test("regionAt finds the region containing a cell", () => {
  assert.equal(merges.regionAt({ row: 0, col: 1 })?.anchor.row, 0);
  assert.equal(merges.regionAt({ row: 2, col: 2 })?.anchor.row, 1); // C3 in B2:C3
  assert.equal(merges.regionAt({ row: 0, col: 0 }), undefined);
});

test("isCovered / isAnchor distinguish anchor from covered cells", () => {
  assert.equal(merges.isAnchor({ row: 0, col: 1 }), true); // B1
  assert.equal(merges.isCovered({ row: 0, col: 2 }), true); // C1
  assert.equal(merges.isCovered({ row: 2, col: 1 }), true); // B3
  assert.equal(merges.isCovered({ row: 3, col: 2 }), true); // C4 in A4:C4
  assert.equal(merges.isCovered({ row: 0, col: 0 }), false); // plain cell
  assert.equal(merges.isAnchor({ row: 0, col: 0 }), false);
});

test("resolveAnchor maps covered cells to their anchor", () => {
  assert.deepEqual(merges.resolveAnchor({ row: 2, col: 2 }), { row: 1, col: 1 });
  assert.deepEqual(merges.resolveAnchor({ row: 3, col: 1 }), { row: 3, col: 0 });
  assert.deepEqual(merges.resolveAnchor({ row: 0, col: 0 }), { row: 0, col: 0 });
});

test("overlapping merge regions are rejected", () => {
  assert.throws(
    () =>
      new MergeMap([
        { anchor: { row: 0, col: 0 }, rows: 2, cols: 2 },
        { anchor: { row: 1, col: 1 }, rows: 1, cols: 2 },
      ]),
    /overlapping merge regions/,
  );
});
