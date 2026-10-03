import { test } from "node:test";
import assert from "node:assert/strict";
import { DominatorTree, type Edge } from "../src/dominator.ts";

const diamond: Edge[] = [
  ["S", "A"],
  ["S", "B"],
  ["A", "T"],
  ["B", "T"],
];

test("diamond: idom(T) is the fork node S", () => {
  const dt = new DominatorTree("S", diamond);
  assert.equal(dt.idom("T"), "S");
  assert.equal(dt.idom("A"), "S");
  assert.equal(dt.idom("B"), "S");
  assert.equal(dt.idom("S"), null);
});

test("diamond: neither A nor B dominates T", () => {
  const dt = new DominatorTree("S", diamond);
  assert.equal(dt.dominates("A", "T"), false);
  assert.equal(dt.dominates("B", "T"), false);
  assert.equal(dt.dominates("S", "T"), true);
  assert.equal(dt.dominates("T", "T"), true);
});

test("incremental add: bypass edge S->T keeps idom(T) = S", () => {
  const dt = new DominatorTree("S", diamond);
  dt.addEdge("S", "T");
  assert.equal(dt.idom("T"), "S");
  assert.equal(dt.dominates("A", "T"), false);
});

test("incremental remove: deleting B->T shifts idom(T) to A", () => {
  const dt = new DominatorTree("S", diamond);
  dt.removeEdge("B", "T");
  assert.equal(dt.idom("T"), "A");
  assert.equal(dt.dominates("A", "T"), true);
  assert.equal(dt.dominates("B", "T"), false);
});

test("counterexample: path to T avoiding A in the diamond", () => {
  const dt = new DominatorTree("S", diamond);
  const witness = dt.counterexample("A", "T");
  assert.ok(witness, "expected a counterexample path");
  assert.deepEqual(witness, ["S", "B", "T"]);
  assert.ok(dt.isValidWitness(witness!, "T", "A"));
});

test("counterexample: none exists once A truly dominates T", () => {
  const dt = new DominatorTree("S", diamond);
  dt.removeEdge("B", "T");
  assert.equal(dt.counterexample("A", "T"), null);
});

test("counterexample: bypass edge S->T yields direct witness", () => {
  const dt = new DominatorTree("S", diamond);
  dt.addEdge("S", "T");
  const witness = dt.counterexample("A", "T");
  assert.deepEqual(witness, ["S", "T"]);
});

test("full acceptance sequence: add bypass then delete B->T", () => {
  const dt = new DominatorTree("S", diamond);
  assert.equal(dt.idom("T"), "S");

  dt.addEdge("S", "T");
  assert.equal(dt.idom("T"), "S");

  dt.removeEdge("B", "T");
  // T is now reachable via S->A->T and S->T, so idom(T) falls back to S.
  assert.equal(dt.idom("T"), "S");
  // But A still does not dominate T; the bypass edge is the witness.
  assert.deepEqual(dt.counterexample("A", "T"), ["S", "T"]);
});

test("unreachable nodes have no dominators and no counterexample", () => {
  const dt = new DominatorTree("S", diamond);
  dt.removeEdge("A", "T");
  dt.removeEdge("B", "T");
  assert.equal(dt.isReachable("T"), false);
  assert.equal(dt.idom("T"), null);
  assert.equal(dt.counterexample("A", "T"), null);
});

test("linear chain: each node is idom of the next", () => {
  const dt = new DominatorTree("n1", [
    ["n1", "n2"],
    ["n2", "n3"],
    ["n3", "n4"],
  ]);
  assert.equal(dt.idom("n2"), "n1");
  assert.equal(dt.idom("n3"), "n2");
  assert.equal(dt.idom("n4"), "n3");
});

test("incremental result matches full recomputation from scratch", () => {
  const edges: Edge[] = [
    ["S", "A"],
    ["S", "B"],
    ["A", "C"],
    ["B", "C"],
    ["C", "T"],
    ["A", "T"],
  ];
  const inc = new DominatorTree("S", edges);
  inc.removeEdge("A", "C");
  inc.addEdge("B", "T");
  inc.removeEdge("C", "T");

  const fresh = new DominatorTree("S", [
    ["S", "A"],
    ["S", "B"],
    ["B", "C"],
    ["A", "T"],
    ["B", "T"],
  ]);

  for (const n of fresh.nodes()) {
    assert.equal(inc.idom(n), fresh.idom(n), `idom mismatch at ${n}`);
    assert.deepEqual(
      [...inc.dominators(n)].sort(),
      [...fresh.dominators(n)].sort(),
      `dom-set mismatch at ${n}`,
    );
  }
});
