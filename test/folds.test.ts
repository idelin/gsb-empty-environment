import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  buildFoldTree,
  flattenFoldTree,
  normalizeFolds,
  validateFolds,
} from "../src/folds.ts";

describe("normalizeFolds", () => {
  it("merges partially intersecting regions into their union", () => {
    assert.deepEqual(
      normalizeFolds([
        { start: 2, end: 8 },
        { start: 5, end: 12 },
      ]),
      [{ start: 2, end: 12 }],
    );
  });

  it("merges transitive chains of intersections", () => {
    assert.deepEqual(
      normalizeFolds([
        { start: 0, end: 4 },
        { start: 3, end: 7 },
        { start: 6, end: 10 },
      ]),
      [{ start: 0, end: 10 }],
    );
  });

  it("keeps disjoint and properly nested regions untouched", () => {
    const input = [
      { start: 20, end: 25 },
      { start: 0, end: 10 },
      { start: 3, end: 6 },
    ];
    assert.deepEqual(normalizeFolds(input), [
      { start: 0, end: 10 },
      { start: 3, end: 6 },
      { start: 20, end: 25 },
    ]);
  });

  it("drops duplicates and empty regions", () => {
    assert.deepEqual(
      normalizeFolds([
        { start: 1, end: 5 },
        { start: 1, end: 5 },
        { start: 9, end: 9 },
      ]),
      [{ start: 1, end: 5 }],
    );
  });

  it("merges partial intersections and keeps legal nesting", () => {
    // [3,12) partially intersects [0,10) -> union [0,12); [2,4) stays nested.
    assert.deepEqual(
      normalizeFolds([
        { start: 0, end: 10 },
        { start: 2, end: 4 },
        { start: 3, end: 12 },
      ]),
      [
        { start: 0, end: 12 },
        { start: 2, end: 4 },
      ],
    );
  });
});

describe("validateFolds", () => {
  it("accepts disjoint and nested regions", () => {
    const sorted = validateFolds([
      { start: 3, end: 6 },
      { start: 0, end: 10 },
      { start: 20, end: 25 },
    ]);
    assert.deepEqual(sorted, [
      { start: 0, end: 10 },
      { start: 3, end: 6 },
      { start: 20, end: 25 },
    ]);
  });

  it("rejects partial intersection with a descriptive error", () => {
    assert.throws(
      () =>
        validateFolds([
          { start: 0, end: 5 },
          { start: 3, end: 8 },
        ]),
      /partially intersect/,
    );
  });
});

describe("buildFoldTree", () => {
  it("nests contained regions and preserves collapsed flags", () => {
    const roots = buildFoldTree(
      [
        { start: 0, end: 20 },
        { start: 2, end: 8 },
        { start: 4, end: 6 },
        { start: 10, end: 15 },
        { start: 25, end: 30 },
      ],
      [true, false, true, false, true],
    );
    assert.equal(roots.length, 2);
    assert.equal(roots[0].collapsed, true);
    assert.equal(roots[0].children.length, 2);
    assert.equal(roots[0].children[0].collapsed, false);
    assert.deepEqual(
      roots[0].children[0].children.map((n) => [n.start, n.end, n.collapsed]),
      [[4, 6, true]],
    );
    assert.equal(flattenFoldTree(roots).length, 5);
  });
});

describe("FoldedDocument fold validation", () => {
  it("rejects a partially intersecting addFold", async () => {
    const { FoldedDocument } = await import("../src/document.ts");
    const doc = new FoldedDocument("0123456789");
    doc.addFold(2, 6);
    assert.throws(() => doc.addFold(4, 9), /partially intersect/);
    assert.deepEqual(doc.folds, [{ start: 2, end: 6 }]); // unchanged
  });

  it("rejects duplicate folds", async () => {
    const { FoldedDocument } = await import("../src/document.ts");
    const doc = new FoldedDocument("0123456789");
    doc.addFold(2, 6);
    assert.throws(() => doc.addFold(2, 6), /Duplicate/);
  });

  it("setFolds with normalize merges instead of rejecting", async () => {
    const { FoldedDocument } = await import("../src/document.ts");
    const doc = new FoldedDocument("0123456789ABCDEF");
    doc.setFolds(
      [
        { start: 2, end: 8 },
        { start: 5, end: 11 },
      ],
      { normalize: true },
    );
    assert.deepEqual(doc.folds, [{ start: 2, end: 11 }]);
    assert.throws(() => doc.setFolds([{ start: 2, end: 8 }, { start: 5, end: 11 }]));
  });
});
