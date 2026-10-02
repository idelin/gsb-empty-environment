/**
 * Acceptance scenario:
 *   - initial max flow of the network is 10
 *   - the key edge a->t is reduced from 5 to 2
 *   - 3 units of flow are withdrawn along reverse augmenting tracks,
 *     an alternative path recovers 1 unit, final max flow is 8
 *   - the min-cut certificate (cut edges + node partition) is printed
 *
 * Network:
 *       [a] --5--> [t]
 *      /  | \       ^
 *   5/    |2 \     / 6
 *   [s]   v   \   /
 *    \   [b] --5-- (b->t capacity 6, 5 used initially)
 *     \         ^
 *      ---5-----
 */
import { IncrementalMaxFlow, type CutCertificate } from "./maxflow.ts";

function printCertificate(label: string, cert: CutCertificate): void {
  console.log(`\n=== ${label} ===`);
  console.log(`max flow:        ${cert.maxFlow}`);
  console.log(`source side (S): { ${cert.sourceSide.join(", ")} }`);
  console.log(`sink side (T):   { ${cert.sinkSide.join(", ")} }`);
  console.log(`cut edges (S->T):`);
  for (const e of cert.cutEdges) {
    console.log(`  ${e.u} -> ${e.v}  capacity=${e.capacity} flow=${e.flow}`);
  }
  console.log(`cut capacity:    ${cert.cutCapacity}`);
}

const mf = new IncrementalMaxFlow("s", "t");
mf.addEdge("s", "a", 5);
mf.addEdge("s", "b", 5);
const keyEdge = mf.addEdge("a", "t", 5); // key edge, id tracked for update
mf.addEdge("b", "t", 6);
mf.addEdge("a", "b", 2);

printCertificate("initial network", mf.computeMaxFlow());

const report = mf.updateCapacities([{ edge: keyEdge, capacity: 2 }]);
console.log(`\n=== capacity change: a -> t reduced 5 -> 2 ===`);
console.log(`flow withdrawn along reverse tracks: ${report.withdrawn}`);
console.log(`flow recovered via alternative paths: ${report.reaugmented}`);
printCertificate("after adjustment", report.certificate);

console.log(`\nfinal edge flows:`);
for (const e of mf.snapshot()) {
  console.log(`  ${e.u} -> ${e.v}  flow=${e.flow}/${e.capacity}`);
}
