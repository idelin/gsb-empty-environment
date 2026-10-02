import { test } from "node:test";
import assert from "node:assert/strict";
import { WeightedGraph } from "../src/graph.ts";
import { approximateBetweenness, brandes, edgeKey, shortestPath } from "../src/betweenness.ts";

function starGraph(leafCount: number): WeightedGraph {
  const g = new WeightedGraph();
  for (let i = 1; i <= leafCount; i++) g.addEdge("C", `L${i}`);
  return g;
}

test("star topology: center carries all inter-leaf traffic", () => {
  const leaves = 5;
  const { node } = brandes(starGraph(leaves));
  assert.equal(node.get("C"), (leaves * (leaves - 1)) / 2);
  for (let i = 1; i <= leaves; i++) assert.equal(node.get(`L${i}`), 0);
});

test("path graph: middle node has highest betweenness", () => {
  const g = new WeightedGraph();
  g.addEdge("A", "B");
  g.addEdge("B", "C");
  g.addEdge("C", "D");
  g.addEdge("D", "E");
  const { node } = brandes(g);
  assert.equal(node.get("C"), 4);
  assert.equal(node.get("B"), 3);
  assert.equal(node.get("D"), 3);
  assert.equal(node.get("A"), 0);
  assert.equal(node.get("E"), 0);
});

test("dumbbell: bridge edge and bridge endpoints dominate", () => {
  const g = new WeightedGraph();
  g.addEdge("A1", "A2");
  g.addEdge("A2", "A3");
  g.addEdge("A1", "A3");
  g.addEdge("B1", "B2");
  g.addEdge("B2", "B3");
  g.addEdge("B1", "B3");
  g.addEdge("A2", "B2");
  const { node, edge } = brandes(g);
  const bridgeScore = edge.get(edgeKey("A2", "B2"))!;
  assert.equal(bridgeScore, 9);
  for (const [key, score] of edge) {
    if (key !== edgeKey("A2", "B2")) assert.ok(score < bridgeScore, `${key} should be below bridge`);
  }
  assert.ok(node.get("A2")! > node.get("A1")!);
  assert.ok(node.get("B2")! > node.get("B1")!);
});

test("weights redirect traffic: heavier path lowers betweenness", () => {
  const g = new WeightedGraph();
  g.addEdge("S", "X", 1);
  g.addEdge("X", "T", 1);
  g.addEdge("S", "Y", 5);
  g.addEdge("Y", "T", 5);
  const light = brandes(g).node;
  assert.ok(light.get("X")! > light.get("Y")!);

  g.setWeight("S", "X", 10);
  g.setWeight("X", "T", 10);
  const heavy = brandes(g).node;
  assert.ok(heavy.get("Y")! > heavy.get("X")!);
});

test("approximation with all nodes as samples equals exact result", () => {
  const g = starGraph(6);
  g.addEdge("L1", "L2", 2);
  const exact = brandes(g);
  const approx = approximateBetweenness(g, g.nodeCount(), 7);
  for (const [n, v] of exact.node) {
    assert.ok(Math.abs(approx.node.get(n)! - v) < 1e-9, `node ${n}`);
  }
});

test("approximation preserves top-1 hub on star+mesh hybrid", () => {
  const g = new WeightedGraph();
  const routers = ["R1", "R2", "R3", "R4"];
  for (let i = 0; i < routers.length; i++) {
    for (let j = i + 1; j < routers.length; j++) g.addEdge(routers[i], routers[j], 1.5);
  }
  for (const r of routers) g.addEdge("GW", r, 1);
  for (let i = 1; i <= 6; i++) g.addEdge(`L${i}`, "GW", 1);

  const exactTop = [...brandes(g).node.entries()].sort((a, b) => b[1] - a[1])[0][0];
  const approxTop = [...approximateBetweenness(g, 6, 123).node.entries()].sort(
    (a, b) => b[1] - a[1],
  )[0][0];
  assert.equal(exactTop, "GW");
  assert.equal(approxTop, "GW");
});

test("shortestPath honors weights and returns null when disconnected", () => {
  const g = new WeightedGraph();
  g.addEdge("A", "B", 1);
  g.addEdge("B", "C", 1);
  g.addEdge("A", "C", 5);
  assert.deepEqual(shortestPath(g, "A", "C"), ["A", "B", "C"]);
  g.addNode("Z");
  assert.equal(shortestPath(g, "A", "Z"), null);
});
