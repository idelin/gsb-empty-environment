import { test } from "node:test";
import assert from "node:assert/strict";
import { WeightedGraph } from "../src/graph.ts";
import { BottleneckMonitor } from "../src/monitor.ts";

function buildHybridNetwork(): WeightedGraph {
  const g = new WeightedGraph();
  const routers = ["R1", "R2", "R3", "R4"];
  const leaves = ["L1", "L2", "L3", "L4", "L5", "L6"];
  for (let i = 0; i < routers.length; i++) {
    for (let j = i + 1; j < routers.length; j++) g.addEdge(routers[i], routers[j], 1.5);
  }
  for (const r of routers) g.addEdge("GW", r, 1);
  for (const l of leaves) g.addEdge(l, "GW", 1);
  for (const l of leaves) g.addEdge(l, "AGG", 3);
  g.addEdge("AGG", "R2", 2);
  g.addEdge("AGG", "R3", 2);
  return g;
}

test("hybrid network: central gateway GW is the top bottleneck", () => {
  const monitor = new BottleneckMonitor(buildHybridNetwork(), { k: 5 });
  const top = monitor.topK();
  assert.equal(top[0].node, "GW");
  assert.ok(top[0].score > top[1].score);
  const topEdge = monitor.topKEdges(1)[0];
  assert.ok(topEdge.edge.includes("GW"));
});

test("dynamic edge-weight update refreshes top-K ranking", () => {
  const g = new WeightedGraph();
  g.addEdge("A1", "G1", 1);
  g.addEdge("A2", "G1", 1);
  g.addEdge("G1", "B1", 1);
  g.addEdge("A1", "G2", 1);
  g.addEdge("A2", "G2", 1);
  g.addEdge("G2", "B1", 1);
  g.addEdge("B1", "B2", 1);

  const monitor = new BottleneckMonitor(g, { k: 4 });
  const before = monitor.topK().map((e) => e.node);
  assert.ok(before.includes("G1") && before.includes("G2"));
  const g1Before = monitor.scoreOf("G1");

  const after = monitor.updateEdgeWeight("A1", "G1", 10);
  monitor.updateEdgeWeight("A2", "G1", 10);
  monitor.updateEdgeWeight("G1", "B1", 10);
  assert.ok(monitor.scoreOf("G1") < g1Before, "G1 should lose centrality");
  assert.ok(monitor.scoreOf("G2") > monitor.scoreOf("G1"), "traffic shifts to G2");
  assert.notDeepEqual(
    after.map((e) => e.node),
    before,
  );
});

test("gateway failure: report names AGG as primary reroute alternative", () => {
  const monitor = new BottleneckMonitor(buildHybridNetwork(), { k: 5 });
  const report = monitor.simulateNodeFailure("GW");

  assert.equal(report.failedNode, "GW");
  assert.equal(report.previousTopK[0].node, "GW");
  assert.ok(!report.newTopK.some((e) => e.node === "GW"));
  assert.equal(report.reroute.primaryAlternative, "AGG");
  assert.equal(report.newTopK[0].node, "AGG");
  assert.equal(report.biggestGainers[0].node, "AGG");
  assert.ok(report.reroute.affectedPairs > 0);
  assert.equal(report.reroute.disconnectedPairs, 0);
});

test("reroute suggestions avoid the failed node and stay connected", () => {
  const monitor = new BottleneckMonitor(buildHybridNetwork(), { k: 5 });
  const report = monitor.simulateNodeFailure("GW");
  assert.ok(report.reroute.sampleReroutes.length > 0);
  for (const r of report.reroute.sampleReroutes) {
    assert.ok(r.oldPath.includes("GW"), "old path should transit GW");
    assert.ok(!r.newPath.includes("GW"), "new path must avoid GW");
    assert.equal(r.newPath[0], r.from);
    assert.equal(r.newPath[r.newPath.length - 1], r.to);
  }
  const totalShare = report.reroute.alternatives.reduce((a, b) => a + b.share, 0);
  assert.ok(Math.abs(totalShare - 1) < 1e-9);
});

test("graph and scores are fully restored after failure simulation", () => {
  const graph = buildHybridNetwork();
  const monitor = new BottleneckMonitor(graph, { k: 5 });
  const before = monitor.topK();
  monitor.simulateNodeFailure("GW");
  assert.deepEqual(monitor.topK(), before);
  assert.ok(graph.hasEdge("GW", "R1"));
  assert.ok(graph.hasEdge("L6", "GW"));
});

test("leaf failure has no reroute alternative for its own traffic", () => {
  const monitor = new BottleneckMonitor(buildHybridNetwork(), { k: 5 });
  const report = monitor.simulateNodeFailure("L1");
  assert.equal(report.reroute.affectedPairs, 0);
  assert.equal(report.reroute.primaryAlternative, null);
});
