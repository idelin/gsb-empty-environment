import test from "node:test";
import assert from "node:assert/strict";
import { Editor } from "../src/editor.ts";
import { computeIndentEdits, computeUnindentEdits, touchedLines } from "../src/indent.ts";
import { applyEdits } from "../src/edits.ts";

test("overlapping selections sharing a line indent that line only once", () => {
  const text = "foo bar\nbaz\n";
  // Three selections all touching line 0, one also touching line 1.
  const sels = [
    { anchor: 0, head: 3 },
    { anchor: 2, head: 5 },
    { anchor: 4, head: 11 },
  ];
  const edits = computeIndentEdits(text, sels);
  assert.equal(edits.length, 2); // one edit per line, not per selection
  assert.equal(applyEdits(text, edits), "    foo bar\n    baz\n");
});

test("fully contained duplicate selections still indent once", () => {
  const text = "abc";
  const sels = [
    { anchor: 0, head: 3 },
    { anchor: 1, head: 2 },
    { anchor: 1, head: 1 },
  ];
  assert.equal(applyEdits(text, computeIndentEdits(text, sels)), "    abc");
});

test("selection ending exactly at a line start does not touch that line", () => {
  const text = "aaa\nbbb\n";
  const sels = [{ anchor: 1, head: 4 }]; // ends at start of line 1
  assert.deepEqual(touchedLines(text, sels), [0]);
  // A caret (empty selection) at a line start does touch that line.
  assert.deepEqual(touchedLines(text, [{ anchor: 4, head: 4 }]), [1]);
});

test("indent skips empty lines but indents whitespace-only lines", () => {
  const text = "a\n\n \nb";
  const sels = [{ anchor: 0, head: text.length }];
  assert.equal(applyEdits(text, computeIndentEdits(text, sels)), "    a\n\n     \n    b");
});

test("indent with useTabs inserts a single tab", () => {
  assert.equal(
    applyEdits("x\ny", computeIndentEdits("x\ny", [{ anchor: 0, head: 3 }], { useTabs: true })),
    "\tx\n\ty",
  );
});

test("unindent removes at most one tab stop", () => {
  const run = (line: string) =>
    applyEdits(line, computeUnindentEdits(line, [{ anchor: 0, head: line.length }]));
  assert.equal(run("        x"), "    x"); // 8 -> 4
  assert.equal(run("         x"), "        x"); // 9 -> 8 (one stop only)
  assert.equal(run("    x"), "x"); // 4 -> 0
  assert.equal(run("   x"), "x"); // 3 -> 0 (less than one stop)
  assert.equal(run("x"), "x"); // nothing to remove
  assert.equal(run(""), "");
});

test("unindent computes existing tabs by visual column", () => {
  const run = (line: string) =>
    applyEdits(line, computeUnindentEdits(line, [{ anchor: 0, head: line.length }]));
  assert.equal(run("\t\tx"), "\tx"); // 8 -> 4
  assert.equal(run("\tx"), "x"); // 4 -> 0
  assert.equal(run("  \tx"), "x"); // "  \t" is visually 4 -> 0
  assert.equal(run(" \t x"), "    x"); // visually 5 -> 4, padded back to the stop
  assert.equal(run("\t x"), "    x"); // visually 5 -> 4
  assert.equal(run(" \t\tx"), "\tx"); // visually 8 -> 4
  assert.equal(run("      x"), "    x"); // 6 -> 4
});

test("unindent lands exactly on the previous tab stop", () => {
  const run = (line: string) =>
    applyEdits(line, computeUnindentEdits(line, [{ anchor: 0, head: line.length }]));
  assert.equal(run("     x"), "    x"); // 5 -> 4
  assert.equal(run("       x"), "    x"); // 7 -> 4
  assert.equal(run("\t   x"), "    x"); // 7 -> 4
});

test("selections are mapped through indent and unindent", () => {
  const ed = new Editor("foo\nbar\n", [
    { anchor: 0, head: 2 }, // line 0
    { anchor: 6, head: 6 }, // caret inside line 1
  ]);
  ed.indent();
  assert.equal(ed.text, "    foo\n    bar\n");
  assert.deepEqual(ed.selections, [
    { anchor: 4, head: 6 },
    { anchor: 14, head: 14 },
  ]);
  ed.unindent();
  assert.equal(ed.text, "foo\nbar\n");
  assert.deepEqual(ed.selections, [
    { anchor: 0, head: 2 },
    { anchor: 6, head: 6 },
  ]);
});

test("cursors inside removed whitespace clamp to the line start", () => {
  const ed = new Editor("    foo", [{ anchor: 2, head: 2 }]);
  ed.unindent();
  assert.equal(ed.text, "foo");
  assert.deepEqual(ed.selections, [{ anchor: 0, head: 0 }]);
});

test("undo restores text byte-for-byte and selections exactly", () => {
  const original = " \t foo\tbar\n\n\t\tbaz 中文\n";
  const sels = [
    { anchor: 3, head: 9 },
    { anchor: 12, head: 2 },
  ];
  const ed = new Editor(original, sels);
  const before = Buffer.from(original, "utf8");
  ed.indent();
  ed.unindent();
  ed.indent({ useTabs: true });
  while (ed.canUndo) assert.equal(ed.undo(), true);
  assert.equal(ed.undo(), false);
  assert.ok(Buffer.from(ed.text, "utf8").equals(before));
  assert.deepEqual(ed.selections, sels);
});
