import test from "node:test";
import assert from "node:assert/strict";
import { anchor, makeNode } from "../src/model.ts";
import { applyBatch } from "../src/batch.ts";
import { markSequence } from "../src/flat.ts";
import type { Doc } from "../src/model.ts";

test("overlapping edits compose in original coordinates", () => {
  const doc: Doc = { nodes: [makeNode("abcdef", [], [], "n")] };
  const { doc: out } = applyBatch(doc, [
    { kind: "add", start: 0, end: 4, mark: "bold" },
    { kind: "add", start: 2, end: 6, mark: "italic" },
  ]);
  assert.deepEqual(markSequence(out), [
    ["bold"],
    ["bold"],
    ["bold", "italic"],
    ["bold", "italic"],
    ["italic"],
    ["italic"],
  ]);
});

test("add and remove of the same mark in one batch cancel out", () => {
  const doc: Doc = { nodes: [makeNode("abcd", [], [], "n")] };
  const { doc: out } = applyBatch(doc, [
    { kind: "add", start: 0, end: 4, mark: "bold" },
    { kind: "remove", start: 1, end: 3, mark: "bold" },
  ]);
  assert.deepEqual(markSequence(out), [["bold"], [], [], ["bold"]]);
});

test("undo restores node identity and anchors exactly", () => {
  const doc: Doc = {
    nodes: [
      makeNode("hello", ["bold"], [anchor("A", "left", 5)], "first"),
      makeNode("", [], [anchor("B", "right", 0)], "empty-anchored"),
      makeNode("world", [], [], "last"),
    ],
  };
  const tx = applyBatch(doc, [
    { kind: "add", start: 0, end: 10, mark: "italic" },
    { kind: "remove", start: 2, end: 4, mark: "bold" },
  ]);
  const restored = tx.undo();
  assert.deepEqual(restored, doc);
  assert.deepEqual(
    restored.nodes.map((n) => n.id),
    ["first", "empty-anchored", "last"],
  );
  assert.equal(restored.nodes[0].anchors[0].id, "A");
  assert.equal(restored.nodes[1].anchors[0].id, "B");
});

test("untouched nodes keep their identity through a batch", () => {
  const doc: Doc = {
    nodes: [makeNode("ab", [], [], "keep1"), makeNode("cd", ["italic"], [], "keep2")],
  };
  const { doc: out } = applyBatch(doc, [{ kind: "add", start: 0, end: 1, mark: "bold" }]);
  const ids = out.nodes.map((n) => n.id);
  assert.ok(ids.includes("keep2")); // untouched node keeps its id
  assert.ok(ids.includes("keep1")); // first piece of the touched node keeps its id
});
