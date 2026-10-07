import { test } from "node:test";
import assert from "node:assert/strict";
import {
  FocusTree,
  DuplicateIdError,
  UnknownIdError,
  type NodeSpec,
} from "../src/focus-tree.ts";

test("positive tabIndex ordered by value, ties broken by document order", () => {
  const tree = new FocusTree([
    { id: "a", tabIndex: 2 },
    { id: "b", tabIndex: 1 },
    {
      id: "group",
      children: [
        { id: "c", tabIndex: 2 },
        { id: "d", tabIndex: 1 },
      ],
    },
    { id: "e", tabIndex: 3 },
  ]);
  assert.deepEqual(tree.traversalOrder(), ["b", "d", "a", "c", "e"]);
});

test("zero tabIndex and bare focusable nodes follow document order after positives", () => {
  const tree = new FocusTree([
    { id: "z1", tabIndex: 0 },
    { id: "p", tabIndex: 5 },
    { id: "z2", focusable: true },
    { id: "wrap", children: [{ id: "z3", tabIndex: 0 }] },
  ]);
  assert.deepEqual(tree.traversalOrder(), ["p", "z1", "z2", "z3"]);
});

test("negative tabIndex is programmatic-only, never in traversal order", () => {
  const tree = new FocusTree([
    { id: "neg", tabIndex: -1 },
    { id: "zero", tabIndex: 0 },
    { id: "pos", tabIndex: 1 },
  ]);
  assert.deepEqual(tree.traversalOrder(), ["pos", "zero"]);
  assert.deepEqual(tree.programmaticOnly(), ["neg"]);
  assert.equal(tree.classify("neg"), "programmatic");
  assert.equal(tree.isFocusable("neg"), true);
});

test("hidden ancestor makes the whole subtree unfocusable", () => {
  const tree = new FocusTree([
    {
      id: "hidden-root",
      hidden: true,
      children: [
        { id: "inner-pos", tabIndex: 1 },
        { id: "inner-zero", tabIndex: 0 },
        { id: "inner-neg", tabIndex: -1 },
      ],
    },
    { id: "visible", tabIndex: 0 },
  ]);
  assert.deepEqual(tree.traversalOrder(), ["visible"]);
  assert.deepEqual(tree.programmaticOnly(), []);
  assert.equal(tree.classify("inner-pos"), "unfocusable");
  assert.equal(tree.classify("inner-neg"), "unfocusable");
});

test("disabled group blocks subtree except explicitly exempt nodes", () => {
  const tree = new FocusTree([
    {
      id: "fieldset",
      disabled: true,
      children: [
        { id: "blocked", tabIndex: 0 },
        {
          id: "exempt-node",
          exempt: true,
          tabIndex: 0,
          children: [{ id: "exempt-child", tabIndex: 0 }],
        },
      ],
    },
    { id: "outside", tabIndex: 0 },
  ]);
  assert.deepEqual(tree.traversalOrder(), ["exempt-node", "exempt-child", "outside"]);
  assert.equal(tree.classify("blocked"), "unfocusable");
  assert.equal(tree.classify("fieldset"), "unfocusable");
});

test("exemption does not rescue nodes from a hidden ancestor", () => {
  const tree = new FocusTree([
    {
      id: "hidden-disabled",
      hidden: true,
      disabled: true,
      children: [{ id: "exempt", exempt: true, tabIndex: 0 }],
    },
  ]);
  assert.equal(tree.classify("exempt"), "unfocusable");
});

test("duplicate ids are rejected at construction, insertion, and rename", () => {
  assert.throws(
    () => new FocusTree([{ id: "x" }, { id: "wrap", children: [{ id: "x" }] }]),
    DuplicateIdError,
  );

  const tree = new FocusTree([{ id: "a" }, { id: "b", children: [{ id: "c" }] }]);
  assert.throws(() => tree.addNode(null, { id: "a" }), DuplicateIdError);
  assert.throws(() => tree.addNode("b", { id: "new", children: [{ id: "c" }] }), DuplicateIdError);
  assert.throws(() => tree.renameId("a", "c"), DuplicateIdError);

  tree.renameId("a", "a2");
  assert.equal(tree.has("a2"), true);
  assert.equal(tree.has("a"), false);
});

test("dynamic tabIndex updates move nodes between traversal groups", () => {
  const tree = new FocusTree([
    { id: "one", tabIndex: 0 },
    { id: "two", tabIndex: 0 },
    { id: "three", tabIndex: 0 },
  ]);
  assert.deepEqual(tree.traversalOrder(), ["one", "two", "three"]);

  tree.updateAttributes("two", { tabIndex: 1 });
  assert.deepEqual(tree.traversalOrder(), ["two", "one", "three"]);

  tree.updateAttributes("two", { tabIndex: -1 });
  assert.deepEqual(tree.traversalOrder(), ["one", "three"]);
  assert.deepEqual(tree.programmaticOnly(), ["two"]);

  tree.updateAttributes("two", { tabIndex: null });
  assert.deepEqual(tree.traversalOrder(), ["one", "three"]);
  assert.equal(tree.classify("two"), "unfocusable");

  tree.updateAttributes("two", { focusable: true });
  assert.deepEqual(tree.traversalOrder(), ["one", "two", "three"]);
});

test("dynamic hidden/disabled/exempt updates re-expand the full tree", () => {
  const tree = new FocusTree([
    {
      id: "panel",
      children: [
        { id: "a", tabIndex: 0 },
        { id: "b", tabIndex: 0 },
      ],
    },
    { id: "c", tabIndex: 0 },
  ]);
  assert.deepEqual(tree.traversalOrder(), ["a", "b", "c"]);

  tree.updateAttributes("panel", { hidden: true });
  assert.deepEqual(tree.traversalOrder(), ["c"]);

  tree.updateAttributes("panel", { hidden: false, disabled: true });
  assert.deepEqual(tree.traversalOrder(), ["c"]);

  tree.updateAttributes("b", { exempt: true });
  assert.deepEqual(tree.traversalOrder(), ["b", "c"]);

  tree.updateAttributes("panel", { disabled: false });
  assert.deepEqual(tree.traversalOrder(), ["a", "b", "c"]);
});

test("structural updates: add and remove keep traversal order consistent", () => {
  const tree = new FocusTree([{ id: "root", children: [{ id: "a", tabIndex: 0 }] }]);
  tree.addNode("root", { id: "b", tabIndex: 1 });
  tree.addNode(null, { id: "tail", tabIndex: 0 });
  assert.deepEqual(tree.traversalOrder(), ["b", "a", "tail"]);

  tree.removeNode("root");
  assert.deepEqual(tree.traversalOrder(), ["tail"]);
  assert.equal(tree.has("a"), false);
  assert.throws(() => tree.updateAttributes("a", { hidden: true }), UnknownIdError);
});

test("document order is depth-first across nesting levels", () => {
  const spec: NodeSpec[] = [
    { id: "r1", tabIndex: 0, children: [
      { id: "r1c1", tabIndex: 0, children: [{ id: "r1c1g1", tabIndex: 0 }] },
      { id: "r1c2", tabIndex: 0 },
    ] },
    { id: "r2", tabIndex: 0 },
  ];
  const tree = new FocusTree(spec);
  assert.deepEqual(tree.traversalOrder(), ["r1", "r1c1", "r1c1g1", "r1c2", "r2"]);
});
