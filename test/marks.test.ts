import test from "node:test";
import assert from "node:assert/strict";
import { anchor, makeNode } from "../src/model.ts";
import { addMark, removeMark, applyMarkEdits, LinkNestingError } from "../src/marks.ts";
import { equivalent, flatten, markSequence, renderChars } from "../src/flat.ts";
import { isNormalized } from "../src/normalize.ts";
import type { Doc } from "../src/model.ts";

test("addMark across nodes splits then merges back", () => {
  const doc: Doc = {
    nodes: [makeNode("foo", ["bold"], [], "a"), makeNode("bar", ["italic"], [], "b")],
  };
  const out = addMark(doc, 0, 6, "underline");
  assert.ok(isNormalized(out));
  assert.equal(renderChars(out), "foobar");
  assert.deepEqual(markSequence(out), Array(6).fill(["bold", "italic", "underline"]).map((m, i) =>
    i < 3 ? ["bold", "underline"] : ["italic", "underline"],
  ));
  // covering everything with the same mark merges into a single node
  const all = addMark(doc, 0, 6, "bold");
  assert.equal(all.nodes.length, 2); // "foo" stays, "bar" gains bold -> still differs by italic
  const unified = addMark(out, 0, 6, "bold");
  assert.ok(isNormalized(unified));
});

test("add then remove the same mark restores an equivalent doc", () => {
  const doc: Doc = {
    nodes: [
      makeNode("hello", ["bold"], [anchor("A", "right", 2)], "a"),
      makeNode(" world", [], [], "b"),
    ],
  };
  const toggled = removeMark(addMark(doc, 1, 9, "italic"), 1, 9, "italic");
  assert.ok(equivalent(doc, toggled));
});

test("removeMark on a subrange splits a node into up to three pieces", () => {
  const doc: Doc = { nodes: [makeNode("abcdef", ["bold"], [], "n")] };
  const out = removeMark(doc, 2, 4, "bold");
  assert.deepEqual(out.nodes.map((n) => n.text), ["ab", "cd", "ef"]);
  assert.deepEqual(
    out.nodes.map((n) => [...n.marks]),
    [["bold"], [], ["bold"]],
  );
  // identity: first piece keeps the original id, later pieces derive from it
  assert.deepEqual(out.nodes.map((n) => n.id), ["n", "n~1", "n~2"]);
});

test("anchors follow their affinity when a split lands on them", () => {
  const doc: Doc = {
    nodes: [
      makeNode("hello", [], [anchor("L", "left", 2), anchor("R", "right", 2)], "n"),
    ],
  };
  const out = addMark(doc, 2, 4, "bold");
  assert.deepEqual(out.nodes.map((n) => n.text), ["he", "ll", "o"]);
  // left-affinity anchor stays with the left piece, right-affinity moves on
  assert.deepEqual(out.nodes[0].anchors.map((a) => a.id), ["L"]);
  assert.deepEqual(out.nodes[1].anchors.map((a) => a.id), ["R"]);
  assert.equal(out.nodes[0].anchors[0].offset, 2);
  assert.equal(out.nodes[1].anchors[0].offset, 0);
  // flat ground truth: both anchors remain at boundary 2
  const flat = flatten(out);
  assert.deepEqual(
    flat.boundaries[2].map((a) => a.id).sort(),
    ["L", "R"],
  );
});

test("empty anchored node survives mark operations over its position", () => {
  const doc: Doc = {
    nodes: [
      makeNode("ab", [], [], "l"),
      makeNode("", [], [anchor("K", "right", 0)], "keep"),
      makeNode("cd", [], [], "r"),
    ],
  };
  const out = addMark(doc, 0, 4, "bold");
  assert.ok(equivalent(doc, removeMark(out, 0, 4, "bold")));
  const flat = flatten(out);
  assert.deepEqual(flat.boundaries[2].map((a) => a.id), ["K"]);
});

test("link marks cannot nest", () => {
  const doc: Doc = { nodes: [makeNode("abcdef", ["link:https://a"], [], "n")] };
  assert.throws(() => addMark(doc, 1, 5, "link:https://b"), LinkNestingError);
  // non-overlapping link application is fine
  const ok = addMark(doc, 3, 6, "bold");
  assert.equal(renderChars(ok), "abcdef");
});

test("validateDoc rejects nodes carrying two link marks", () => {
  const doc: Doc = {
    nodes: [makeNode("x", ["link:a", "link:b"], [], "bad")],
  };
  assert.throws(() => applyMarkEdits(doc, []), /nested links/);
});

test("invalid ranges are rejected", () => {
  const doc: Doc = { nodes: [makeNode("abc", [], [], "n")] };
  assert.throws(() => addMark(doc, -1, 2, "bold"), RangeError);
  assert.throws(() => addMark(doc, 2, 1, "bold"), RangeError);
  assert.throws(() => addMark(doc, 0, 4, "bold"), RangeError);
});
