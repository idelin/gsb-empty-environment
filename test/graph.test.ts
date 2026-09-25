import { test } from "node:test";
import assert from "node:assert/strict";
import { Graph, GraphError } from "../src/graph.ts";

test("attach/detach maintains subtree stats along ancestor chain only", () => {
  const g = new Graph();
  g.addEntity("R");
  g.addEntity("A");
  g.addEntity("B");
  g.setSelfWeight("A", 2);
  g.setSelfWeight("B", 3);
  g.attach("A", "R");
  g.attach("B", "A");
  assert.deepEqual(g.get("R").stats, { nodeCount: 3, weightSum: 5 });
  assert.deepEqual(g.get("A").stats, { nodeCount: 2, weightSum: 5 });
  assert.deepEqual(g.get("B").stats, { nodeCount: 1, weightSum: 3 });

  g.drainTouched();
  g.detach("B");
  assert.deepEqual(g.get("R").stats, { nodeCount: 2, weightSum: 2 });
  assert.deepEqual(g.get("A").stats, { nodeCount: 1, weightSum: 2 });
  assert.deepEqual(g.get("B").stats, { nodeCount: 1, weightSum: 3 });
  // only the affected ancestor chain was touched
  assert.deepEqual([...g.drainTouched()].sort(), ["A", "R"]);
});

test("cycle: attaching A under its descendant B is rejected atomically", () => {
  const g = new Graph();
  for (const id of ["A", "B", "C"]) g.addEntity(id);
  g.setSelfWeight("A", 1);
  g.setSelfWeight("B", 2);
  g.setSelfWeight("C", 3);
  g.attach("B", "A");
  g.attach("C", "B");
  const before = JSON.stringify([...g.nodes.values()].map((n) => [n.id, n.parentId, n.stats]));
  assert.throws(() => g.attach("A", "C"), (e: unknown) => {
    assert.ok(e instanceof GraphError);
    assert.equal((e as GraphError).code, "CYCLE");
    return true;
  });
  const after = JSON.stringify([...g.nodes.values()].map((n) => [n.id, n.parentId, n.stats]));
  assert.equal(after, before, "graph and stats must be unchanged");
});

test("self-cycle is rejected", () => {
  const g = new Graph();
  g.addEntity("A");
  assert.throws(() => g.attach("A", "A"), /cycle/);
});

test("orphan queue: child before parent, reattach keeps identity", () => {
  const g = new Graph();
  g.addEntity("D");
  g.setSelfWeight("D", 4);
  g.markOrphan("D", "M");
  assert.equal(g.orphans.get("D"), "M");
  assert.equal(g.get("D").parentId, null);

  g.addEntity("M");
  const attached = g.reattachOrphans("M");
  assert.deepEqual(attached, ["D"]);
  assert.equal(g.orphans.size, 0);
  assert.equal(g.get("D").parentId, "M");
  assert.deepEqual(g.get("M").stats, { nodeCount: 2, weightSum: 4 });
});

test("removeEntity keeps children as orphans (no silent cascade)", () => {
  const g = new Graph();
  for (const id of ["R", "M", "D", "E"]) g.addEntity(id);
  g.attach("M", "R");
  g.attach("D", "M");
  g.attach("E", "M");
  g.setSelfWeight("D", 4);
  const orphaned = g.removeEntity("M");
  assert.deepEqual(orphaned.sort(), ["D", "E"]);
  assert.equal(g.has("M"), false);
  assert.equal(g.orphans.get("D"), "M");
  assert.equal(g.orphans.get("E"), "M");
  // children keep their own subtree stats and identity
  assert.deepEqual(g.get("D").stats, { nodeCount: 1, weightSum: 4 });
  assert.deepEqual(g.get("R").stats, { nodeCount: 1, weightSum: 0 });
  // restoring the parent reattaches the same children
  g.addEntity("M");
  assert.deepEqual(g.reattachOrphans("M").sort(), ["D", "E"]);
  assert.deepEqual(g.get("M").stats, { nodeCount: 3, weightSum: 4 });
});
