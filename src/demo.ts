import { WeightedGraph } from "./graph.ts";
import { BottleneckMonitor, type BottleneckEntry, type BottleneckShiftReport } from "./monitor.ts";

function buildHybridNetwork(): WeightedGraph {
  const g = new WeightedGraph();
  const routers = ["R1", "R2", "R3", "R4"];
  const leaves = ["L1", "L2", "L3", "L4", "L5", "L6"];

  for (let i = 0; i < routers.length; i++) {
    for (let j = i + 1; j < routers.length; j++) {
      g.addEdge(routers[i], routers[j], 1.5);
    }
  }
  for (const r of routers) g.addEdge("GW", r, 1);
  for (const l of leaves) g.addEdge(l, "GW", 1);

  for (const l of leaves) g.addEdge(l, "AGG", 3);
  g.addEdge("AGG", "R2", 2);
  g.addEdge("AGG", "R3", 2);
  return g;
}

function printTopK(title: string, entries: BottleneckEntry[]): void {
  console.log(title);
  entries.forEach((e, i) => {
    console.log(`  ${String(i + 1).padStart(2)}. ${e.node.padEnd(4)} score=${e.score.toFixed(2)}`);
  });
}

function printReport(report: BottleneckShiftReport): void {
  console.log(`\n=== Bottleneck shift report: failure of ${report.failedNode} ===`);
  printTopK("Top-K before failure:", report.previousTopK);
  printTopK("Top-K after failure:", report.newTopK);
  console.log("Biggest gainers (betweenness delta):");
  for (const g of report.biggestGainers) {
    console.log(`  ${g.node.padEnd(4)} +${g.delta.toFixed(2)}`);
  }
  const r = report.reroute;
  console.log(`Affected node pairs: ${r.affectedPairs}, disconnected: ${r.disconnectedPairs}`);
  console.log(`Primary reroute alternative: ${r.primaryAlternative ?? "none"}`);
  console.log("Alternative ranking:");
  for (const a of r.alternatives.slice(0, 5)) {
    console.log(
      `  ${a.node.padEnd(4)} redirected=${String(a.redirectedFlows).padStart(3)} share=${(a.share * 100).toFixed(1)}%`,
    );
  }
  console.log("Sample reroutes:");
  for (const s of r.sampleReroutes.slice(0, 5)) {
    console.log(`  ${s.from} -> ${s.to}: [${s.oldPath.join(">")}]  =>  [${s.newPath.join(">")}]`);
  }
}

const graph = buildHybridNetwork();
const monitor = new BottleneckMonitor(graph, { k: 5 });

console.log("=== Hybrid star+mesh network: 12 nodes (GW, AGG, R1-R4, L1-L6) ===");
printTopK("Initial top-5 bottleneck nodes:", monitor.topK());
console.log("Top-3 bottleneck edges:");
for (const e of monitor.topKEdges(3)) {
  console.log(`  ${e.edge[0]}-${e.edge[1]} score=${e.score.toFixed(2)}`);
}

console.log("\n=== Dynamic edge-weight update: GW-R1 congestion (1 -> 8) ===");
const updated = monitor.updateEdgeWeight("GW", "R1", 8);
printTopK("Top-5 after weight update:", updated);
monitor.updateEdgeWeight("GW", "R1", 1);

const report = monitor.simulateNodeFailure("GW");
printReport(report);

console.log("\n=== Graph restored after simulation ===");
printTopK("Top-5 after restore:", monitor.topK());
