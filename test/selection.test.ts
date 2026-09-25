import { test } from "node:test";
import assert from "node:assert/strict";
import { Grid } from "../src/grid.ts";
import { MergeMap } from "../src/merge.ts";
import { expandToMerges } from "../src/selection.ts";
import { fixtureGrid } from "../src/fixtures/nodeTree.ts";

// Fixture merges: B1:C1, B2:C3, A4:C4 on a 4x4 grid.
const grid = new Grid(fixtureGrid);

test("selection touching a merge expands to cover the whole region", () => {
  // Drag A1..B2 touches B1:C1 and B2:C3.
  const rect = grid.select({ anchor: { row: 0, col: 0 }, focus: { row: 1, col: 1 } });
  assert.deepEqual(rect, { top: 0, left: 0, bottom: 2, right: 2 });
});

test("selection fully inside a merge covers the whole region", () => {
  const rect = grid.select({ anchor: { row: 1, col: 2 }, focus: { row: 2, col: 2 } });
  assert.deepEqual(rect, { top: 1, left: 1, bottom: 2, right: 2 }); // B2:C3
});

test("chained merges expand to a fixed point", () => {
  // B3..B4 touches B2:C3; covering that reaches row 3, which touches
  // A4:C4; expansion must keep going until nothing more intersects.
  const rect = grid.select({ anchor: { row: 2, col: 1 }, focus: { row: 3, col: 1 } });
  assert.deepEqual(rect, { top: 1, left: 0, bottom: 3, right: 2 });
});

test("expansion is idempotent at the fixed point", () => {
  const once = grid.select({ anchor: { row: 0, col: 0 }, focus: { row: 1, col: 1 } });
  const twice = expandToMerges(once, grid.merges);
  assert.deepEqual(twice, once);
});

test("selection avoiding all merges stays put", () => {
  const rect = grid.select({ anchor: { row: 0, col: 0 }, focus: { row: 0, col: 0 } });
  assert.deepEqual(rect, { top: 0, left: 0, bottom: 0, right: 0 });
});

test("a long merge chain terminates", () => {
  // Diagonal staircase: regions never overlap, but each expansion step
  // grows the union rect far enough to wake the next region. Stored in
  // reverse order so a single pass cannot reach the fixed point.
  const chain = new MergeMap([
    { anchor: { row: 3, col: 3 }, rows: 1, cols: 2 }, // (3,3)-(3,4)
    { anchor: { row: 2, col: 2 }, rows: 1, cols: 2 }, // (2,2)-(2,3)
    { anchor: { row: 1, col: 1 }, rows: 1, cols: 2 }, // (1,1)-(1,2)
    { anchor: { row: 0, col: 0 }, rows: 1, cols: 2 }, // (0,0)-(0,1)
  ]);
  const rect = expandToMerges({ top: 0, left: 0, bottom: 3, right: 0 }, chain);
  assert.deepEqual(rect, { top: 0, left: 0, bottom: 3, right: 4 });
});
