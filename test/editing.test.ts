import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { FoldedDocument } from "../src/document.ts";

const TEXT = "alpha beta gamma delta epsilon";
//  outer: [6, 22) = "beta gamma delta", inner: [11, 16) = "gamma"

function makeDoc(): FoldedDocument {
  const doc = new FoldedDocument(TEXT);
  doc.addFold(6, 22);
  doc.addFold(11, 16);
  return doc;
}

describe("applyVisibleEdit", () => {
  it("shifts placeholders when text is inserted before them", () => {
    const doc = makeDoc(); // "alpha ⋯ epsilon"
    const res = doc.applyVisibleEdit(0, 0, ">> ");
    assert.equal(doc.text, ">> alpha beta gamma delta epsilon");
    assert.equal(doc.visibleText, ">> alpha ⋯ epsilon");
    assert.deepEqual(doc.folds, [
      { start: 9, end: 25 },
      { start: 14, end: 19 },
    ]);
    assert.equal(res.delta, 3);
  });

  it("moves placeholders when text before them is deleted", () => {
    const doc = makeDoc();
    doc.applyVisibleEdit(0, 6, ""); // delete "alpha "
    assert.equal(doc.text, "beta gamma delta epsilon");
    assert.equal(doc.visibleText, "⋯ epsilon");
    assert.deepEqual(doc.folds, [
      { start: 0, end: 16 },
      { start: 5, end: 10 },
    ]);
  });

  it("deleting a placeholder removes the whole folded range and its folds", () => {
    const doc = makeDoc(); // "alpha ⋯ epsilon", placeholder at [6,7)
    const res = doc.applyVisibleEdit(6, 7, "");
    assert.deepEqual([res.originalStart, res.originalEnd], [6, 22]);
    assert.equal(doc.text, "alpha  epsilon");
    assert.equal(doc.visibleText, "alpha  epsilon");
    assert.deepEqual(doc.folds, []);
  });

  it("replacing a placeholder substitutes new text and drops the fold", () => {
    const doc = makeDoc();
    doc.applyVisibleEdit(6, 7, "MIDDLE");
    assert.equal(doc.text, "alpha MIDDLE epsilon");
    assert.deepEqual(doc.folds, []);
  });

  it("edits inside an expanded outer fold shift inner folds and keep nesting", () => {
    const doc = makeDoc();
    doc.expandAt(6); // "alpha beta ⋯ delta epsilon", inner placeholder at [11,12)
    // Insert "XX " before "beta" (visible offset 6).
    doc.applyVisibleEdit(6, 6, "XX ");
    assert.equal(doc.text, "alpha XX beta gamma delta epsilon");
    assert.equal(doc.visibleText, "alpha XX beta ⋯ delta epsilon");
    const tree = doc.foldTree;
    assert.equal(tree.length, 1);
    assert.equal(tree[0].collapsed, false);
    assert.deepEqual(
      [tree[0].children[0].start, tree[0].children[0].end],
      [14, 19],
    );
    assert.equal(tree[0].children[0].collapsed, true); // nested state kept
  });

  it("grows a fold when typing inside it (expanded outer)", () => {
    const doc = makeDoc();
    doc.expandAt(6);
    // Type right after "gamma" (original offset 16, inside the outer fold).
    const v = doc.toVisibleOffset(16);
    doc.applyVisibleEdit(v, v, "!");
    assert.equal(doc.text, "alpha beta gamma! delta epsilon");
    assert.deepEqual(doc.folds, [
      { start: 6, end: 23 },
      { start: 11, end: 16 },
    ]);
  });

  it("keeps collapsed state after edits around a placeholder", () => {
    const doc = makeDoc();
    doc.applyVisibleEdit(0, 0, ">");
    doc.applyVisibleEdit(doc.visibleText.length, doc.visibleText.length, "<");
    const tree = doc.foldTree;
    assert.equal(tree[0].collapsed, true);
    assert.equal(tree[0].children[0].collapsed, true);
    assert.equal(doc.visibleText, ">alpha ⋯ epsilon<");
  });

  it("removes a fold partially overlapped by a deletion but keeps children", () => {
    const doc = new FoldedDocument("0123456789ABCDEF");
    doc.addFold(2, 14); // outer
    doc.addFold(4, 8); // inner child
    doc.expandAt(2);
    // Outer expanded, inner collapsed.
    assert.equal(doc.visibleText, "0123⋯89ABCDEF");
    // Delete from original offset 10 to 15 (visible 5..10): cuts the outer
    // fold's tail but leaves the inner fold untouched.
    const vs = doc.toVisibleOffset(10);
    const ve = doc.toVisibleOffset(15);
    doc.applyVisibleEdit(vs, ve, "");
    assert.equal(doc.text, "0123456789F");
    assert.deepEqual(doc.folds, [{ start: 4, end: 8 }]); // child survives
  });

  it("maps a cross-placeholder selection edit to the full original range", () => {
    const doc = new FoldedDocument("aa bbbb cc dddd ee");
    doc.addFold(3, 7);
    doc.addFold(11, 15);
    // visible "aa ⋯ cc ⋯ ee": select [3, 9) covering both placeholders.
    const res = doc.applyVisibleEdit(3, 9, "X");
    assert.deepEqual([res.originalStart, res.originalEnd], [3, 15]);
    assert.equal(doc.text, "aa X ee");
    assert.deepEqual(doc.folds, []);
  });
});
