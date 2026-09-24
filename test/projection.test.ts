import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { FoldedDocument } from "../src/document.ts";

const TEXT = "alpha beta gamma delta epsilon";
//             0123456789...
//  outer: [6, 22)  = "beta gamma delta"
//  inner: [11, 16) = "gamma"

function makeDoc(): FoldedDocument {
  const doc = new FoldedDocument(TEXT);
  doc.addFold(6, 22); // outer
  doc.addFold(11, 16); // inner
  return doc;
}

describe("projection", () => {
  it("replaces a collapsed fold with the placeholder", () => {
    const doc = new FoldedDocument(TEXT);
    doc.addFold(6, 22);
    assert.equal(doc.visibleText, "alpha ⋯ epsilon");
  });

  it("hides inner placeholders while the outer fold is collapsed", () => {
    const doc = makeDoc();
    assert.equal(doc.visibleText, "alpha ⋯ epsilon");
    assert.equal(doc.visibleText.split("⋯").length - 1, 1);
  });

  it("shows inner placeholders again after expanding the outer fold", () => {
    const doc = makeDoc();
    doc.expandAt(6);
    assert.equal(doc.visibleText, "alpha beta ⋯ delta epsilon");
  });

  it("keeps the inner fold collapsed after the outer fold is expanded", () => {
    const doc = makeDoc();
    doc.expandAt(6);
    const tree = doc.foldTree;
    assert.equal(tree[0].collapsed, false);
    assert.equal(tree[0].children[0].collapsed, true);
  });

  it("re-collapses inner state independently", () => {
    const doc = makeDoc();
    doc.expandAt(6);
    doc.expandAt(11);
    assert.equal(doc.visibleText, "alpha beta gamma delta epsilon");
    doc.collapseAt(12);
    assert.equal(doc.visibleText, "alpha beta ⋯ delta epsilon");
  });

  it("supports a custom placeholder", () => {
    const doc = new FoldedDocument(TEXT, { placeholder: "[[...]]" });
    doc.addFold(6, 22);
    assert.equal(doc.visibleText, "alpha [[...]] epsilon");
  });
});

describe("offset mapping", () => {
  it("maps visible offsets to original offsets across a placeholder", () => {
    const doc = makeDoc();
    const visible = doc.visibleText; // "alpha ⋯ epsilon"
    assert.equal(doc.toOriginalOffset(0), 0);
    assert.equal(doc.toOriginalOffset(6), 6); // placeholder start
    assert.equal(doc.toOriginalOffset(7), 22); // placeholder end
    assert.equal(doc.toOriginalOffset(visible.length), TEXT.length);
    assert.equal(doc.toOriginalOffset(8), 23); // 'e' of epsilon
  });

  it("maps original offsets to visible offsets, hidden ones to the placeholder", () => {
    const doc = makeDoc();
    assert.equal(doc.toVisibleOffset(0), 0);
    assert.equal(doc.toVisibleOffset(6), 6); // fold start -> placeholder start
    assert.equal(doc.toVisibleOffset(22), 7); // fold end -> placeholder end
    assert.equal(doc.toVisibleOffset(13), 6); // hidden interior -> nearer edge
    assert.equal(doc.toVisibleOffset(15), 7);
    assert.equal(doc.toVisibleOffset(TEXT.length), doc.visibleText.length);
  });

  it("round-trips offsets in visible text segments", () => {
    const doc = makeDoc();
    doc.expandAt(6); // "alpha beta ⋯ delta epsilon"
    const visible = doc.visibleText;
    for (let v = 0; v <= visible.length; v++) {
      const o = doc.toOriginalOffset(v);
      assert.equal(doc.toVisibleOffset(o), v, `visible offset ${v}`);
    }
  });

  it("maps a selection spanning a placeholder to the full original range", () => {
    const doc = makeDoc(); // "alpha ⋯ epsilon"
    // Select from inside "alpha" to inside "epsilon", crossing the placeholder.
    assert.deepEqual(doc.toOriginalRange(2, 9), [2, 24]);
    // Selecting exactly the placeholder selects the whole fold.
    assert.deepEqual(doc.toOriginalRange(6, 7), [6, 22]);
  });

  it("maps a selection spanning two placeholders to both full folds", () => {
    const doc = new FoldedDocument("aa bbbb cc dddd ee");
    doc.addFold(3, 7); // "bbbb"
    doc.addFold(11, 15); // "dddd"
    assert.equal(doc.visibleText, "aa ⋯ cc ⋯ ee");
    assert.deepEqual(doc.toOriginalRange(3, 9), [3, 15]);
  });

  it("maps original ranges back to visible ranges", () => {
    const doc = makeDoc();
    assert.deepEqual(doc.toVisibleRange(6, 22), [6, 7]);
    assert.deepEqual(doc.toVisibleRange(0, 5), [0, 5]);
  });
});
