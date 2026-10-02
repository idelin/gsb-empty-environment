/**
 * Runnable demo of the acceptance scenario:
 *
 *   node1: move /dirA -> /dirB        (concurrent)
 *   node2: move /dirB -> /dirA        (concurrent)
 *
 * After merging, the deterministic total order (lamport, replica, seq)
 * reverts the later operation, breaking the topological ring so that no
 * directory is orphaned.
 *
 * Run: npm run demo   (or: node examples/demo.ts)
 */

import { AcyclicTreeCrdt } from "../src/tree.ts";

function printTree(r: AcyclicTreeCrdt, label: string): void {
  console.log(`\n== ${label} ==`);
  const states = r.state();
  const byId = new Map(states.map((s) => [s.id, s]));
  const walk = (id: string, depth: number): void => {
    const n = byId.get(id)!;
    console.log(`${"  ".repeat(depth)}${n.name}  [${id}]`);
    for (const child of n.children) walk(child, depth + 1);
  };
  walk("root", 0);
}

// 1. node1 creates /dirA and /dirB; both replicas sync.
const node1 = new AcyclicTreeCrdt("node1");
const node2 = new AcyclicTreeCrdt("node2");
const dirA = node1.create("dirA");
const dirB = node1.create("dirB");
node2.merge(node1);
node1.merge(node2);
printTree(node1, "after initial sync (both replicas agree)");

// 2. Concurrent conflicting moves — no communication in between.
const op1 = node1.move(dirA, dirB);
const op2 = node2.move(dirB, dirA);
console.log("\nconcurrent ops:");
console.log(`  node1: move /dirA -> /dirB   ts=${op1.ts.lamport}@${op1.ts.replica}`);
console.log(`  node2: move /dirB -> /dirA   ts=${op2.ts.lamport}@${op2.ts.replica}`);

// 3. Merge in both directions.
node1.merge(node2);
node2.merge(node1);

printTree(node1, "node1 after merge");
printTree(node2, "node2 after merge");

// 4. Arbitration report.
for (const { op, reason } of node1.droppedOps()) {
  const m = op as { node: string; newParent?: string; ts: { lamport: number; replica: string } };
  console.log(
    `\narbitration: reverted op from ${m.ts.replica} ` +
      `(move ${m.node} -> ${m.newParent}): ${reason}`,
  );
}
console.log(`\nnode1 path of dirA: ${node1.pathOf(dirA)}`);
console.log(`node1 path of dirB: ${node1.pathOf(dirB)}`);
console.log(`converged & acyclic: ${node1.allReachable() && node2.allReachable()}`);
