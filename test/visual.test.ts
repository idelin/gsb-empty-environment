import test from "node:test";
import assert from "node:assert/strict";
import { nextTabStop, prevTabStop, tabInsertion, visualColumn } from "../src/visual.ts";

test("visualColumn counts tabs by visual stops", () => {
  assert.equal(visualColumn(""), 0);
  assert.equal(visualColumn("a"), 1);
  assert.equal(visualColumn("\t"), 4);
  assert.equal(visualColumn(" \t"), 4);
  assert.equal(visualColumn("  \t"), 4);
  assert.equal(visualColumn("   \t"), 4);
  assert.equal(visualColumn("\t "), 5);
  assert.equal(visualColumn("\t\t"), 8);
  assert.equal(visualColumn("ab\tc", 3), 4);
  assert.equal(visualColumn("ab\tc"), 5);
  assert.equal(visualColumn(" \t \t"), 8);
});

test("nextTabStop / prevTabStop", () => {
  assert.equal(nextTabStop(0), 4);
  assert.equal(nextTabStop(3), 4);
  assert.equal(nextTabStop(4), 8);
  assert.equal(prevTabStop(0), 0);
  assert.equal(prevTabStop(1), 0);
  assert.equal(prevTabStop(4), 0);
  assert.equal(prevTabStop(5), 4);
  assert.equal(prevTabStop(8), 4);
  assert.equal(prevTabStop(9), 8);
});

test("tabInsertion advances to the next stop", () => {
  assert.equal(tabInsertion(0, false), "    ");
  assert.equal(tabInsertion(2, false), "  ");
  assert.equal(tabInsertion(4, false), "    ");
  assert.equal(tabInsertion(0, true), "\t");
});
