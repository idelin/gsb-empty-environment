import { DominatorTree } from "../src/dominator.ts";

function showIdoms(dt: DominatorTree, label: string): void {
  const parts = dt
    .nodes()
    .filter((n) => dt.isReachable(n))
    .map((n) => `idom(${n})=${dt.idom(n) ?? "∅"}`);
  console.log(`${label}\n  ${parts.join("  ")}`);
}

function showCounterexample(dt: DominatorTree, a: string, b: string): void {
  const path = dt.counterexample(a, b);
  if (path) {
    console.log(`  "${a} dominates ${b}"? NO — counterexample path: ${path.join(" → ")}`);
  } else {
    console.log(`  "${a} dominates ${b}"? YES — no avoiding path exists`);
  }
}

// Diamond: S forks to A and B, joining at T.
const dt = new DominatorTree("S", [
  ["S", "A"],
  ["S", "B"],
  ["A", "T"],
  ["B", "T"],
]);

console.log("=== 1. Diamond CFG: S -> {A, B} -> T ===");
showIdoms(dt, "Initial dominator tree:");
showCounterexample(dt, "A", "T");

console.log("\n=== 2. Add bypass edge S -> T (incremental) ===");
dt.addEdge("S", "T");
showIdoms(dt, "After addEdge(S, T):");
showCounterexample(dt, "A", "T");

console.log("\n=== 3. Remove edge B -> T (incremental) ===");
dt.removeEdge("B", "T");
showIdoms(dt, "After removeEdge(B, T):");
showCounterexample(dt, "A", "T");

console.log("\n=== 4. Remove bypass S -> T as well: A now dominates T ===");
dt.removeEdge("S", "T");
showIdoms(dt, "After removeEdge(S, T):");
showCounterexample(dt, "A", "T");
