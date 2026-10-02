/**
 * Acceptance scenario demo:
 *   node1 moves /dirA under /dirB  while, concurrently,
 *   node2 moves /dirB under /dirA.
 * After merging, the CRDT detects the topological dependency cycle and
 * deterministically undoes the later operation — no directory is lost.
 */
import { Replica, type BaseNode } from "../src/index.ts";

const BASE: BaseNode[] = [
  { id: "root", name: "", parent: null },
  { id: "dirA", name: "dirA", parent: "root" },
  { id: "dirB", name: "dirB", parent: "root" },
  { id: "fileA1", name: "notes.txt", parent: "dirA" },
  { id: "fileB1", name: "report.md", parent: "dirB" },
];

function showTree(r: Replica, label: string): void {
  console.log(`\n${label}`);
  for (const id of ["dirA", "dirB", "fileA1", "fileB1"]) {
    console.log(`  ${id.padEnd(7)} -> ${r.pathOf(id)}`);
  }
}

const node1 = new Replica("node1", BASE);
const node2 = new Replica("node2", BASE);

console.log("=== Initial tree (both replicas identical) ===");
showTree(node1, "node1 view:");

console.log("\n=== Concurrent moves (replicas disconnected) ===");
const op1 = node1.move("dirA", "dirB");
console.log(`node1 issues: move dirA -> under dirB   (lamport=${op1.time}, replica=${op1.replica})`);
const op2 = node2.move("dirB", "dirA");
console.log(`node2 issues: move dirB -> under dirA   (lamport=${op2.time}, replica=${op2.replica})`);

showTree(node1, "node1 local view (before merge):");
showTree(node2, "node2 local view (before merge):");

console.log("\n=== Anti-entropy sync: exchanging operation logs ===");
node1.syncWith(node2);

console.log("\nArbitration verdicts (deterministic total order: time, then replica id):");
for (const v of node1.tree.verdicts) {
  const tag = v.applied ? "APPLIED" : "UNDONE ";
  console.log(
    `  [${tag}] (t=${v.op.time}, ${v.op.replica}) move ${v.op.node} -> ${v.op.parent}` +
      (v.reason ? `\n           reason: ${v.reason}` : ""),
  );
}

showTree(node1, "node1 converged view (after merge):");
showTree(node2, "node2 converged view (after merge):");

const reachable = node1.tree.reachableFrom("root");
const allSafe = [...node1.state.keys()].every((id) => reachable.has(id));
console.log(`\nConvergence check: node1 state === node2 state -> ${
  JSON.stringify([...node1.state]) === JSON.stringify([...node2.state])
}`);
console.log(`No orphaned directories (nothing lost): ${allSafe}`);
console.log(
  `Result: node2's later op was undone; final layout keeps /dirB/dirA — cycle prevented.`,
);
