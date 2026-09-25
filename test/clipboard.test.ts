import { test } from "node:test";
import assert from "node:assert/strict";
import { Grid } from "../src/grid.ts";
import { fixtureGrid } from "../src/fixtures/nodeTree.ts";

// Fixture merges: B1:C1, B2:C3, A4:C4 on a 4x4 grid.
const grid = new Grid(fixtureGrid);

test("copy keeps the rectangular structure with empty placeholders", () => {
  const matrix = grid.copyMatrix({ anchor: { row: 0, col: 0 }, focus: { row: 3, col: 3 } });
  assert.deepEqual(matrix, [
    ["Quarter", "Revenue", "", "Delta"],
    ["Q1", "120.5", "", "4.2%"],
    ["Q2", "", "", "-1.3%"],
    ["Total", "", "", "3.1%"],
  ]);
  // Every row has the same number of slots: the rectangle is intact.
  assert.ok(matrix.every((line) => line.length === 4));
});

test("copy expands the selection over merges before exporting", () => {
  // Drag B1..C2: touches B1:C1 and B2:C3, so rows 1..3 come along.
  const matrix = grid.copyMatrix({ anchor: { row: 0, col: 1 }, focus: { row: 1, col: 2 } });
  assert.deepEqual(matrix, [
    ["Revenue", ""],
    ["120.5", ""],
    ["", ""],
  ]);
});

test("anchor value appears once, covered cells are empty", () => {
  const matrix = grid.copyMatrix({ anchor: { row: 3, col: 0 }, focus: { row: 3, col: 2 } });
  assert.deepEqual(matrix, [["Total", "", ""]]);
});

test("TSV serialization uses tabs and newlines", () => {
  const tsv = grid.copyTSV({ anchor: { row: 0, col: 0 }, focus: { row: 1, col: 1 } });
  assert.equal(tsv, "Quarter\tRevenue\t\nQ1\t120.5\t\nQ2\t\t");
});
