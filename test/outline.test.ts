import { test } from "node:test";
import assert from "node:assert/strict";
import {
  OutlineDocument,
  createNode,
  type FlatEntry,
  type OutlineNode,
} from "../src/outline.ts";

function tree(spec: [string, ...unknown[]][]): OutlineNode[] {
  return spec.map((entry) => {
    const [id, ...rest] = entry;
    const children = (rest[0] ?? []) as [string, ...unknown[]][];
    return createNode(id, id, tree(children));
  });
}

function sampleDoc(): OutlineDocument {
  return new OutlineDocument(
    tree([
      ["A", [["a1"], ["a2", [["a2x"]]]]],
      ["B"],
      ["C", [["c1"]]],
      ["D"],
      ["E"],
    ]),
  );
}

function flatIds(doc: OutlineDocument): string[] {
  return doc.flatten().map((entry: FlatEntry) => entry.id);
}

function assertParentChain(
  doc: OutlineDocument,
  id: string,
  expectedChain: string[],
): void {
  const chain: string[] = [];
  let current = doc.parentOf(id);
  while (current !== null && current !== undefined) {
    chain.push(current);
    const next = doc.parentOf(current);
    current = next === undefined ? null : next;
  }
  assert.deepEqual(chain, expectedChain);
}

test("same-parent move of non-adjacent nodes, target index in pre-removal coordinates", () => {
  const doc = new OutlineDocument(tree([["A"], ["B"], ["C"], ["D"], ["E"]]));
  const result = doc.move(["B", "D"], { parentId: null, index: 5 });
  assert.deepEqual(result, { ok: true });
  assert.deepEqual(flatIds(doc), ["A", "C", "E", "B", "D"]);
  assertParentChain(doc, "B", []);
  assertParentChain(doc, "D", []);
});

test("same-parent move to an earlier index keeps source document order", () => {
  const doc = new OutlineDocument(tree([["A"], ["B"], ["C"], ["D"], ["E"]]));
  const result = doc.move(["E", "C"], { parentId: null, index: 0 });
  assert.deepEqual(result, { ok: true });
  assert.deepEqual(flatIds(doc), ["C", "E", "A", "B", "D"]);
});

test("same-parent move landing between kept siblings adjusts for removals", () => {
  const doc = new OutlineDocument(tree([["A"], ["B"], ["C"], ["D"]]));
  const result = doc.move(["A"], { parentId: null, index: 3 });
  assert.deepEqual(result, { ok: true });
  assert.deepEqual(flatIds(doc), ["B", "C", "A", "D"]);
});

test("cross-parent move preserves pre-order across different parents", () => {
  const doc = sampleDoc();
  const result = doc.move(["c1", "a1", "B"], { parentId: null, index: 0 });
  assert.deepEqual(result, { ok: true });
  assert.deepEqual(flatIds(doc), [
    "a1",
    "B",
    "c1",
    "A",
    "a2",
    "a2x",
    "C",
    "D",
    "E",
  ]);
  assertParentChain(doc, "a1", []);
  assertParentChain(doc, "c1", []);
  assertParentChain(doc, "a2x", ["a2", "A"]);
});

test("move into a deeper parent updates the parent chain", () => {
  const doc = sampleDoc();
  const result = doc.move(["B", "D"], { parentId: "a2", index: 1 });
  assert.deepEqual(result, { ok: true });
  assert.deepEqual(flatIds(doc), [
    "A",
    "a1",
    "a2",
    "a2x",
    "B",
    "D",
    "C",
    "c1",
    "E",
  ]);
  assertParentChain(doc, "B", ["a2", "A"]);
  assertParentChain(doc, "D", ["a2", "A"]);
  assert.equal(doc.isAncestor("A", "B"), true);
});

test("selected descendants are ignored when their ancestor is selected", () => {
  const doc = sampleDoc();
  const result = doc.move(["a2x", "A", "a2"], { parentId: null, index: 5 });
  assert.deepEqual(result, { ok: true });
  assert.deepEqual(flatIds(doc), [
    "B",
    "C",
    "c1",
    "D",
    "E",
    "A",
    "a1",
    "a2",
    "a2x",
  ]);
  assertParentChain(doc, "a2x", ["a2", "A"]);
  assertParentChain(doc, "A", []);
});

test("moving into a selected node is rejected and leaves the document unchanged", () => {
  const doc = sampleDoc();
  const before = doc.flatten();
  const result = doc.move(["a1"], { parentId: "a1", index: 0 });
  assert.deepEqual(result, { ok: false, reason: "target-inside-selection" });
  assert.deepEqual(doc.flatten(), before);
  assert.equal(doc.canUndo, false);
});

test("moving into a descendant of a selected node is rejected atomically", () => {
  const doc = sampleDoc();
  const before = doc.flatten();
  const result = doc.move(["A", "B"], { parentId: "a2", index: 0 });
  assert.deepEqual(result, { ok: false, reason: "target-inside-selection" });
  assert.deepEqual(doc.flatten(), before);
  assert.equal(doc.canUndo, false);
});

test("moving into a descendant selected alongside its ancestor is rejected", () => {
  const doc = sampleDoc();
  const before = doc.flatten();
  const result = doc.move(["A", "a2"], { parentId: "a2x", index: 0 });
  assert.deepEqual(result, { ok: false, reason: "target-inside-selection" });
  assert.deepEqual(doc.flatten(), before);
});

test("unknown selection, unknown target parent, and bad index fail without changes", () => {
  const doc = sampleDoc();
  const before = doc.flatten();
  assert.deepEqual(doc.move(["nope"], { parentId: null, index: 0 }), {
    ok: false,
    reason: "unknown-selection",
  });
  assert.deepEqual(doc.move(["B"], { parentId: "nope", index: 0 }), {
    ok: false,
    reason: "unknown-target-parent",
  });
  assert.deepEqual(doc.move(["B"], { parentId: null, index: 99 }), {
    ok: false,
    reason: "index-out-of-range",
  });
  assert.deepEqual(doc.move(["B"], { parentId: null, index: -1 }), {
    ok: false,
    reason: "index-out-of-range",
  });
  assert.deepEqual(doc.flatten(), before);
  assert.equal(doc.canUndo, false);
});

test("undo restores the exact pre-move structure", () => {
  const doc = sampleDoc();
  const before = doc.flatten();
  doc.move(["c1", "a1", "B"], { parentId: "a2", index: 1 });
  assert.notDeepEqual(doc.flatten(), before);
  assert.equal(doc.undo(), true);
  assert.deepEqual(doc.flatten(), before);
  assertParentChain(doc, "c1", ["C"]);
  assertParentChain(doc, "a1", ["A"]);
  assertParentChain(doc, "B", []);
});

test("undo restores same-parent moves including original indexes", () => {
  const doc = new OutlineDocument(tree([["A"], ["B"], ["C"], ["D"], ["E"]]));
  const before = doc.flatten();
  doc.move(["B", "D"], { parentId: null, index: 5 });
  assert.equal(doc.undo(), true);
  assert.deepEqual(doc.flatten(), before);
});

test("redo reapplies the move after undo", () => {
  const doc = sampleDoc();
  doc.move(["B", "D"], { parentId: "a2", index: 1 });
  const moved = doc.flatten();
  doc.undo();
  assert.equal(doc.redo(), true);
  assert.deepEqual(doc.flatten(), moved);
  assertParentChain(doc, "B", ["a2", "A"]);
});

test("a failed move does not disturb the undo history", () => {
  const doc = sampleDoc();
  const before = doc.flatten();
  doc.move(["B"], { parentId: null, index: 0 });
  doc.move(["A"], { parentId: "a2", index: 0 });
  assert.equal(doc.undo(), true);
  assert.deepEqual(doc.flatten(), before);
  assert.equal(doc.canUndo, false);
});

test("undo on an empty history is a no-op", () => {
  const doc = sampleDoc();
  assert.equal(doc.undo(), false);
  assert.equal(doc.redo(), false);
});
