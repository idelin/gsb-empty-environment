import test from "node:test";
import assert from "node:assert/strict";
import { anchor, makeNode } from "../src/model.ts";
import { normalize, isNormalized } from "../src/normalize.ts";
import { equivalent, flatten, renderChars } from "../src/flat.ts";
import type { Doc } from "../src/model.ts";

test("merges adjacent nodes with equal marks regardless of set order", () => {
  const doc: Doc = {
    nodes: [
      makeNode("foo", ["bold", "italic"], [], "a"),
      makeNode("bar", ["italic", "bold"], [], "b"),
      makeNode("baz", ["bold"], [], "c"),
    ],
  };
  const { doc: out, map } = normalize(doc);
  assert.equal(out.nodes.length, 2);
  assert.equal(out.nodes[0].text, "foobar");
  assert.equal(out.nodes[0].id, "a"); // first node keeps identity
  assert.equal(out.nodes[1].text, "baz");
  // both merged originals point at the same new range
  assert.deepEqual(map[0], { nodeStart: 0, nodeEnd: 1, charStart: 0 });
  assert.deepEqual(map[1], { nodeStart: 0, nodeEnd: 1, charStart: 3 });
  assert.deepEqual(map[2], { nodeStart: 1, nodeEnd: 2, charStart: 6 });
});

test("drops empty nodes without anchors, keeps anchored empty nodes", () => {
  const doc: Doc = {
    nodes: [
      makeNode("ab", [], [], "x"),
      makeNode("", [], [], "empty"),
      makeNode("", [], [anchor("A1", "right", 0)], "anchored"),
      makeNode("cd", [], [], "y"),
    ],
  };
  const { doc: out, map } = normalize(doc);
  assert.equal(out.nodes.length, 3);
  assert.equal(out.nodes[1].id, "anchored");
  assert.equal(out.nodes[1].anchors.length, 1);
  assert.equal(map[1], null); // dropped
  assert.deepEqual(map[2], { nodeStart: 1, nodeEnd: 2, charStart: 2 });
  assert.equal(renderChars(out), "abcd");
});

test("anchored empty node is a merge barrier", () => {
  const doc: Doc = {
    nodes: [
      makeNode("ab", ["bold"], [], "l"),
      makeNode("", ["bold"], [anchor("A", "left", 0)], "mid"),
      makeNode("cd", ["bold"], [], "r"),
    ],
  };
  const { doc: out } = normalize(doc);
  assert.equal(out.nodes.length, 3);
  assert.equal(out.nodes[1].id, "mid");
});

test("merging shifts anchor offsets of the absorbed node", () => {
  const doc: Doc = {
    nodes: [
      makeNode("hello", ["bold"], [anchor("L", "left", 5)], "a"),
      makeNode("world", ["bold"], [anchor("R", "right", 0)], "b"),
    ],
  };
  const { doc: out } = normalize(doc);
  assert.equal(out.nodes.length, 1);
  const [merged] = out.nodes;
  assert.equal(merged.text, "helloworld");
  assert.deepEqual(
    merged.anchors.map((a) => [a.id, a.offset]),
    [
      ["L", 5],
      ["R", 5],
    ],
  );
});

test("normalization is idempotent", () => {
  const doc: Doc = {
    nodes: [
      makeNode("", [], [], "e1"),
      makeNode("a", ["bold"], [], "1"),
      makeNode("b", ["bold"], [], "2"),
      makeNode("", [], [anchor("K", "right", 0)], "keep"),
      makeNode("c", [], [], "3"),
      makeNode("", [], [], "e2"),
    ],
  };
  const once = normalize(doc).doc;
  const twice = normalize(once).doc;
  assert.deepEqual(twice, once);
  assert.ok(isNormalized(once));
  assert.ok(!isNormalized(doc));
});

test("normalization preserves rendered chars, marks and anchors", () => {
  const doc: Doc = {
    nodes: [
      makeNode("he", ["bold", "italic"], [anchor("p", "left", 1)], "1"),
      makeNode("", [], [], "drop"),
      makeNode("llo", ["italic", "bold"], [], "2"),
      makeNode("", [], [anchor("q", "right", 0)], "keep"),
      makeNode("!", [], [], "3"),
    ],
  };
  const { doc: out } = normalize(doc);
  assert.ok(equivalent(doc, out));
  assert.deepEqual(flatten(doc).boundaries, flatten(out).boundaries);
});
