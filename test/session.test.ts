import { test } from "node:test";
import assert from "node:assert/strict";
import { DiffSession } from "../src/session.ts";

test("local edit on one side updates blocks and mapping", () => {
  const a = "alpha\nbeta\ngamma\n";
  const b = "alpha\nbeta\ngamma\n";
  const session = new DiffSession(a, b);
  assert.equal(session.getChangeBlocks().length, 0);

  // Insert "delta\n" before "beta" (UTF-16 offset 6) on side A.
  session.applyEdit("a", { start: 6, end: 6, text: "delta\n" });
  assert.equal(session.getText("a"), "alpha\ndelta\nbeta\ngamma\n");
  assert.deepEqual(session.getChangeBlocks(), [
    { kind: "change", aStart: 1, aEnd: 2, bStart: 1, bEnd: 1 },
  ]);
  assert.equal(session.aToB(0), 0);
  assert.equal(session.aToB(1), null); // new line has no counterpart in B
  assert.equal(session.aToB(2), 1);
  assert.equal(session.aToB(3), 2);
  assert.equal(session.bToA(1), 2);
});

test("reverting an edit removes diff blocks and restores identity mapping", () => {
  const a = "alpha\nbeta\ngamma\n";
  const b = "alpha\nbeta\ngamma\n";
  const session = new DiffSession(a, b);

  session.applyEdit("a", { start: 6, end: 6, text: "delta\n" });
  assert.equal(session.getChangeBlocks().length, 1);

  // Undo: remove the inserted "delta\n" (6 UTF-16 units at offset 6).
  session.applyEdit("a", { start: 6, end: 12, text: "" });
  assert.equal(session.getText("a"), a);
  assert.equal(session.getChangeBlocks().length, 0);
  for (let i = 0; i < 3; i++) {
    assert.equal(session.aToB(i), i);
    assert.equal(session.bToA(i), i);
  }
  // Folding over the restored, fully equal text collapses nothing at
  // reasonable context sizes.
  assert.deepEqual(session.folds(2), []);
});

test("edits on side B are supported symmetrically", () => {
  const session = new DiffSession("a\nb\n", "a\nb\n");
  session.applyEdit("b", { start: 2, end: 2, text: "x\n" });
  assert.deepEqual(session.getChangeBlocks(), [
    { kind: "change", aStart: 1, aEnd: 1, bStart: 1, bEnd: 2 },
  ]);
  assert.equal(session.bToA(1), null);
  assert.equal(session.bToA(2), 1);
});

test("edit offsets must not split a surrogate pair", () => {
  const text = "x😀y\nrest\n";
  const session = new DiffSession(text, text);
  assert.throws(
    () => session.applyEdit("a", { start: 2, end: 2, text: "boom" }),
    /surrogate pair/,
  );
  // Failed edit leaves the session untouched.
  assert.equal(session.getText("a"), text);
  assert.equal(session.getChangeBlocks().length, 0);
});

test("UTF-16 offsets: emoji before the edit point counts as two units", () => {
  const a = "😀\nfoo\nbar\n";
  const session = new DiffSession(a, a);
  // "foo" starts at offset 3 (😀 = 2 units, "\n" = 1).
  session.applyEdit("a", { start: 3, end: 6, text: "baz" });
  assert.equal(session.getText("a"), "😀\nbaz\nbar\n");
  assert.equal(session.aToB(1), null);
  assert.equal(session.bToA(1), null);
  assert.equal(session.aToB(2), 2);
});

test("CRLF documents: offsets and edits account for two-unit terminators", () => {
  const a = "one\r\ntwo\r\n";
  const session = new DiffSession(a, a);
  // Insert "mid\r\n" after the first line; "two" starts at offset 5.
  session.applyEdit("a", { start: 5, end: 5, text: "mid\r\n" });
  assert.equal(session.getText("a"), "one\r\nmid\r\ntwo\r\n");
  assert.equal(session.aToB(1), null);
  assert.equal(session.aToB(2), 1);
});

test("LF vs CRLF with identical content produces no change blocks", () => {
  const session = new DiffSession("a\r\nb\r\nc", "a\nb\nc");
  assert.equal(session.getChangeBlocks().length, 0);
  assert.equal(session.aToB(2), 2);
});

test("repeated blank lines stay stable through the session API", () => {
  const make = () => new DiffSession("\n\n\n\n", "\n\n");
  const expected = [{ kind: "change", aStart: 2, aEnd: 4, bStart: 2, bEnd: 2 }];
  assert.deepEqual(make().getChangeBlocks(), expected);
  assert.deepEqual(make().getChangeBlocks(), expected);
  const session = make();
  assert.equal(session.aToB(0), 0);
  assert.equal(session.aToB(2), null); // deleted blank line: no counterpart
  assert.equal(session.aToB(3), null);
});

test("long texts: folds collapse context around a mid-file change", () => {
  const linesA = Array.from({ length: 1000 }, (_, i) => `row-${i}`);
  const linesB = linesA.slice();
  linesB[500] = "changed";
  const session = new DiffSession(linesA.join("\n"), linesB.join("\n"));

  assert.deepEqual(session.getChangeBlocks(), [
    { kind: "change", aStart: 500, aEnd: 501, bStart: 500, bEnd: 501 },
  ]);
  const folds = session.folds(3);
  assert.equal(folds.length, 2);
  assert.deepEqual(folds[0], { aStart: 3, aEnd: 497, bStart: 3, bEnd: 497, hiddenLines: 494 });
  assert.deepEqual(folds[1], {
    aStart: 504,
    aEnd: 997,
    bStart: 504,
    bEnd: 997,
    hiddenLines: 493,
  });
  // Mapping works through the fold and across the change.
  assert.equal(session.aToB(100), 100);
  assert.equal(session.aToB(500), null);
  assert.equal(session.aToB(900), 900);
});
