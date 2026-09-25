import { test } from "node:test";
import assert from "node:assert/strict";
import { measureText } from "../src/fixtures/glyphs.ts";
import { fixtureGrid } from "../src/fixtures/nodeTree.ts";
import { computeLayout, hitTest } from "../src/layout.ts";

test("text measurement is driven only by the glyph fixture", () => {
  assert.equal(measureText("Q1"), 18); // Q=10, 1=8
  assert.equal(measureText("120.5"), 36); // 8*4 + 4
  assert.equal(measureText("中"), 16); // wide glyph
  assert.equal(measureText(""), 0);
});

test("layout derives column widths and row offsets from the node tree", () => {
  const geometry = computeLayout(fixtureGrid);
  assert.equal(geometry.rows, 4);
  assert.equal(geometry.cols, 4);
  // Column widths: max text width per column + 2 * cellPaddingX (12).
  // col0 "Quarter" = 49 -> 61, col1 "Revenue" = 56 -> 68,
  // col2 empty -> 12, col3 "-1.3%" = 37 -> 49.
  assert.deepEqual(geometry.columnOffsets, [0, 61, 129, 141, 190]);
  // Row height: lineHeight 20 + 2 * cellPaddingY 4 = 28.
  assert.deepEqual(geometry.rowOffsets, [0, 28, 56, 84, 112]);
});

test("hit test maps pixels back to cells without a browser", () => {
  const geometry = computeLayout(fixtureGrid);
  assert.deepEqual(hitTest(geometry, 30, 5), { row: 0, col: 0 });
  assert.deepEqual(hitTest(geometry, 130, 60), { row: 2, col: 2 });
  assert.deepEqual(hitTest(geometry, 189, 111), { row: 3, col: 3 });
  assert.equal(hitTest(geometry, -1, 0), null);
  assert.equal(hitTest(geometry, 0, 112), null);
});
