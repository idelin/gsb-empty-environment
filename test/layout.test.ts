import { test } from "node:test";
import assert from "node:assert/strict";
import { DocumentModel } from "../src/model.ts";
import { BidiLayout } from "../src/layout.ts";
import { fixedMetrics, graphemeTree, mixedTree, nestedTree } from "./fixtures.ts";
import { splitGraphemes } from "../src/grapheme.ts";

test("grapheme clusters: combining marks, ZWJ and flag pairs are atomic", () => {
  assert.deepEqual(splitGraphemes("abc"), ["a", "b", "c"]);
  assert.deepEqual(splitGraphemes("e\u0301a"), ["e\u0301", "a"]);
  assert.deepEqual(splitGraphemes("\u{1F468}\u200D\u{1F469}x"), ["\u{1F468}\u200D\u{1F469}", "x"]);
  assert.deepEqual(splitGraphemes("\u{1F1FA}\u{1F1F8}"), ["\u{1F1FA}\u{1F1F8}"]);

  const model = new DocumentModel(graphemeTree());
  assert.equal(model.length, 4);
  // No caret position exists strictly inside any of the four clusters.
  for (const nodeId of ["t"]) {
    assert.deepEqual(model.clustersOf(nodeId), [
      "e",
      "e\u0301",
      "\u{1F468}\u200D\u{1F469}",
      "\u{1F1FA}\u{1F1F8}",
    ]);
  }
});

test("visual order: LTR text with RTL fragment reverses only the fragment", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  assert.equal(layout.visualText(0), "abcFEDghi");
  const visualIndices = layout.lines[0]!.items.map((item) => item.logicalIndex);
  assert.deepEqual(visualIndices, [0, 1, 2, 5, 4, 3, 6, 7, 8]);
});

test("nested RTL paragraph with LTR embedding", () => {
  const model = new DocumentModel(nestedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  assert.equal(layout.visualText(0), "baWORLDeh");
});

test("same logical offset yields two visual boundaries at a direction split", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  // abc FED ghi: offset 3 is start of RTL run (x=30) and offset 6 its end (x=30)
  const startRun = layout.caretAt(3, "forward");
  const endRun = layout.caretAt(6, "backward");
  assert.equal(startRun.x, 60);
  assert.equal(endRun.x, 30);
  // one logical offset (3) sits at two visual boundaries
  assert.deepEqual(
    layout.caretsAt(3).map((c) => c.x).sort((a, b) => a - b),
    [30, 60],
  );
  assert.deepEqual(
    layout.caretsAt(6).map((c) => c.x).sort((a, b) => a - b),
    [30, 60],
  );
  void endRun;
  // the left direction boundary exposes both logical carets
  const hit = layout.hitTest(0, 30);
  const offsets = hit.carets.map((c) => c.offset).sort((a, b) => a - b);
  assert.deepEqual(offsets, [3, 6]);
});

test("boundary between same-direction runs is a single position", () => {
  const model = new DocumentModel({
    id: "p",
    children: [{ id: "t", text: "ab", clusters: ["a", "b"], levels: [0, 0] }],
  });
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  const hit = layout.hitTest(0, 10);
  assert.equal(hit.carets.length, 1);
  assert.deepEqual(
    hit.carets.map((c) => `${c.offset}/${c.affinity}`),
    ["1/backward"],
  );
});

test("soft wrap: same logical offset appears once per visual line, affinity explicit", () => {
  const model = new DocumentModel(mixedTree());
  // width 10 each, maxWidth 30 -> exactly three clusters per line (visual order)
  const layout = new BidiLayout(model, { maxWidth: 30, metrics: fixedMetrics(10) });
  assert.equal(layout.lines.length, 3);
  assert.deepEqual(
    layout.lines.map((line) => line.items.map((item) => item.text).join("")),
    ["abc", "FED", "ghi"],
  );
  // wrap boundary between logical c(2)/F(5): offset 3 forward lives on line 0
  // trailing? It is the leading edge of F, so it stays with F on line 1.
  const fLeading = layout.caretAt(3, "forward");
  const cTrailing = layout.caretAt(3, "backward");
  assert.equal(fLeading.line, 1);
  assert.equal(cTrailing.line, 0);
  assert.equal(fLeading.x, 30);
  assert.equal(cTrailing.x, 30);
});

test("hit test never collapses multi-valued mapping and marks a primary caret", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  const hit = layout.hitTest(0, 30);
  assert.ok(hit.carets.includes(hit.primary));
  assert.equal(hit.carets.length, 2);
  // deeper attachment (RTL level 1) wins primary
  assert.equal(hit.primary.offset, 6);
  assert.equal(hit.primary.affinity, "backward");
});

test("visual arrow keys move physically; logical arrow keys move by offset", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  // start at logical 0; visual Right walks the physical row a b c F E D g h i
  let caret = layout.firstCaret();
  const visualPath: string[] = [];
  for (let step = 0; step < 6; step++) {
    caret = layout.moveVisual(caret, "right");
    visualPath.push(`${caret.offset}/${caret.affinity}`);
  }
  // after a,b,c we arrive at F's trailing edge: offset 6/backward, then E:5, D:4
  assert.deepEqual(visualPath, [
    "1/forward",
    "2/forward",
    "6/backward",
    "5/backward",
    "4/backward",
    "6/forward",
  ]);
  // logical order steps BACKWARDS while traversing the RTL run visually right
  assert.deepEqual(visualPath.slice(2, 5).map((p) => p.split("/")[0]), ["6", "5", "4"]);
  // entering the run from the left parks on F's trailing edge (offset 6),
  // the OTHER logical caret at the same geometric position (offset 3)
  assert.equal(visualPath[2], "6/backward");
  // moving left visits g then the run's far boundary (offset 3, its forward
  // edge — the dual partner of the backward edge reached when going right),
  // then D,E,F in logical order, finally c's trailing edge
  // move left from the position reached after six right steps (6/forward):
  // the RTL run is read in logical order D,E,F, then c's trailing edge
  const leftPath: string[] = [];
  for (let step = 0; step < 6; step++) {
    caret = layout.moveVisual(caret, "left");
    leftPath.push(`${caret.offset}/${caret.affinity}`);
  }
  assert.deepEqual(leftPath, [
    "4/forward",
    "5/forward",
    "3/backward",
    "2/backward",
    "1/backward",
    "0/forward",
  ]);
  // the 3/forward dual partner is reached one step earlier (from slot6)
  const fromG = layout.moveVisual(layout.caretAt(7, "backward"), "left");
  assert.equal(fromG.offset, 3);
  assert.equal(fromG.affinity, "forward");
  void fromG;
  // the c|F split offers both logical carets from one geometric position
  assert.equal(layout.hitTest(0, 30).carets.length, 2);
  assert.deepEqual(
    layout.hitTest(0, 30).carets.map((c) => c.offset).sort((a, b) => a - b),
    [3, 6],
  );

  // logical forward always increments offset; it jumps visually inside the run
  let logical = layout.firstCaret();
  const logicalPath: number[] = [];
  for (let step = 0; step < 4; step++) {
    logical = layout.moveLogical(logical, "forward");
    logicalPath.push(logical.offset);
  }
  assert.deepEqual(logicalPath, [1, 2, 3, 4]);
});

test("visual movement crosses wrapped lines and home/end hit line extremes", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 30, metrics: fixedMetrics(10) });
  const line0End = layout.moveVisual(layout.firstCaret(), "end");
  assert.equal(line0End.line, 0);
  assert.equal(line0End.x, 30);
  // line end parked on c's trailing edge (logical offset 3, backward)
  assert.equal(line0End.offset, 3);
  assert.equal(line0End.affinity, "backward");
  const wrapped = layout.moveVisual(line0End, "right");
  assert.equal(wrapped.line, 1);
  assert.equal(wrapped.x, 0);
  // line 1 begins with the RTL fragment: its left slot is F's trailing edge
  assert.equal(wrapped.offset, 6);
  assert.equal(wrapped.affinity, "backward");
  // offset 3 still has exactly two distinct geometric carets total
  assert.equal(layout.caretsAt(3).length, 2);
  assert.equal(new Set(layout.caretsAt(3).map((c) => `${c.line}:${c.x}`)).size, 2);
  const home = layout.moveVisual(wrapped, "home");
  assert.equal(home.x, 0);
  assert.equal(home.line, 1);
});

test("local level update bumps version and invalidates old carets/selection", () => {
  const model = new DocumentModel(mixedTree());
  const layout = new BidiLayout(model, { maxWidth: 1000, metrics: fixedMetrics(10) });
  const oldCaret = layout.caretAt(3, "forward");
  assert.equal(layout.version, 0);

  const model2 = model.updateLevels("t", 3, 3, 0); // DEF becomes LTR
  assert.equal(model2.version, 1);
  const layout2 = new BidiLayout(model2, { maxWidth: 1000, metrics: fixedMetrics(10) });
  assert.equal(layout2.visualText(0), "abcDEFghi");

  assert.throws(() => layout2.assertCurrent(oldCaret), /Stale caret/);
  assert.throws(() => layout2.moveVisual(oldCaret, "right"), /Stale caret/);
  assert.equal(layout2.caretAt(3, "forward").layoutVersion, 1);
  assert.notEqual(layout2.caretAt(3, "forward"), oldCaret);

  // old layout still serves its own version-consistent caret
  assert.doesNotThrow(() => layout.assertCurrent(oldCaret));
});

test("offset outside 0..N is rejected and empty document has one caret", () => {
  const empty = new DocumentModel({ id: "p", children: [] });
  const layout = new BidiLayout(empty, { maxWidth: 100, metrics: fixedMetrics(10) });
  assert.equal(layout.caretsAt(0).length, 1);
  assert.throws(() => layout.caretsAt(1));
});
