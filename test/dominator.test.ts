import { test } from "node:test";
import assert from "node:assert/strict";
import { DominatorTree, type NodeId } from "../src/dominator.ts";

function makeDiamond(): DominatorTree {
  return new DominatorTree("S", [
    ["S", "A"],
    ["S", "B"],
    ["A", "T"],
    ["B", "T"],
  ]);
}

function assertValidPath(
  tree: DominatorTree,
  path: NodeId[],
  from: NodeId,
  to: NodeId,
  avoiding: NodeId,
): void {
  assert.equal(path[0], from, "path must start at the entry");
  assert.equal(path[path.length - 1], to, "path must end at the target");
  assert.ok(!path.includes(avoiding), `path must avoid ${avoiding}`);
  const reach = tree.reachable();
  for (const n of path) assert.ok(reach.has(n), `${n} must be reachable`);
}

test("diamond: idom(T) is the entry S", () => {
  const tree = makeDiamond();
  assert.equal(tree.idom("T"), "S");
  assert.equal(tree.idom("A"), "S");
  assert.equal(tree.idom("B"), "S");
  assert.equal(tree.idom("S"), null);
  assert.ok(tree.dominates("S", "T"));
  assert.ok(!tree.dominates("A", "T"));
  assert.ok(!tree.dominates("B", "T"));
});

test("incremental insert S->T (bypass edge): idom(T) stays S", () => {
  const tree = makeDiamond();
  tree.addEdge("S", "T");
  assert.equal(tree.idom("T"), "S");
  assert.ok(tree.dominates("S", "T"));
});

test("incremental deletes: after removing S->T and B->T, idom(T) jumps to A", () => {
  const tree = makeDiamond();
  tree.addEdge("S", "T");
  assert.equal(tree.idom("T"), "S");
  tree.removeEdge("S", "T");
  assert.equal(tree.idom("T"), "S"); // back to the plain diamond
  tree.removeEdge("B", "T");
  assert.equal(tree.idom("T"), "A"); // only S->A->T remains
  assert.ok(tree.dominates("A", "T"));
});

test("counterexample: path from S to T avoiding A in the diamond", () => {
  const tree = makeDiamond();
  const path = tree.proveNotDominates("A", "T");
  assert.ok(path !== null, "a counterexample path must exist");
  assertValidPath(tree, path, "S", "T", "A");
  assert.deepEqual(path, ["S", "B", "T"]);
});

test("no counterexample when dominance holds", () => {
  const tree = makeDiamond();
  assert.equal(tree.proveNotDominates("S", "T"), null);
  tree.removeEdge("B", "T");
  assert.equal(tree.proveNotDominates("A", "T"), null);
});

test("counterexample after incremental insert: A still does not dominate T", () => {
  const tree = makeDiamond();
  tree.addEdge("S", "T");
  const path = tree.proveNotDominates("A", "T");
  assert.ok(path !== null);
  assertValidPath(tree, path, "S", "T", "A");
});

test("deletion can make nodes unreachable", () => {
  const tree = makeDiamond();
  tree.removeEdge("S", "B");
  assert.ok(!tree.reachable().has("B"));
  assert.equal(tree.idom("B"), null);
  assert.equal(tree.idom("T"), "A"); // only S->A->T remains
  tree.removeEdge("B", "T");
  assert.equal(tree.idom("T"), "A");
});

test("larger graph: chain through join node", () => {
  const tree = new DominatorTree("S", [
    ["S", "A"],
    ["S", "B"],
    ["A", "J"],
    ["B", "J"],
    ["J", "T"],
  ]);
  assert.equal(tree.idom("J"), "S");
  assert.equal(tree.idom("T"), "J");
  tree.addEdge("S", "J");
  assert.equal(tree.idom("J"), "S");
  assert.equal(tree.idom("T"), "J");
  tree.removeEdge("J", "T");
  assert.equal(tree.idom("T"), null); // T unreachable now
});
