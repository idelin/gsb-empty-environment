import { Cluster } from "../src/index.ts";

/**
 * Demo of the acceptance scenario:
 *   1. 5-node cluster elects node-0 as leader via quorum lease.
 *   2. Network splits into {node-0, node-1} and {node-2, node-3, node-4}.
 *   3. The old leader cannot renew (2 < quorum 3), loses its lease at expiry
 *      and stops serving writes.
 *   4. The 3-node partition elects a new leader and keeps serving.
 *   5. The partition heals; the cluster converges back to a single leader.
 */

const cluster = new Cluster({ size: 5 });
const cursor = new Map(cluster.nodes.map((n) => [n.id, 0]));

function printEvents(): void {
  for (const node of cluster.nodes) {
    const seen = cursor.get(node.id)!;
    for (const event of node.events.slice(seen)) console.log(`  ${event}`);
    cursor.set(node.id, node.events.length);
  }
}

function status(): string {
  return cluster.nodes
    .map((n) => {
      const lease = n.lease();
      const tail = lease ? ` lease<=${lease.expiresAt}ms` : "";
      return `${n.id}=${n.state}(t${n.term})${tail}`;
    })
    .join("  ");
}

function phase(title: string): void {
  console.log(`\n=== ${title} (t=${cluster.clock.now()}ms) ===`);
}

function tryWrite(nodeId: string, value: string): void {
  const node = cluster.node(nodeId);
  try {
    const ack = node.write(value);
    console.log(`  WRITE "${value}" -> ACCEPTED by ${nodeId} (fencing term=${ack.term})`);
  } catch (error) {
    console.log(`  WRITE "${value}" -> REJECTED by ${nodeId}: ${(error as Error).message}`);
  }
}

phase("Phase 1: quorum election of node-0");
cluster.elect("node-0");
printEvents();
cluster.run(400);
console.log(status());
tryWrite("node-0", "order#1");

phase("Phase 2: partition {node-0,node-1} | {node-2,node-3,node-4}");
cluster.partition([
  ["node-0", "node-1"],
  ["node-2", "node-3", "node-4"],
]);
console.log("  network cut in both directions between the two groups");
tryWrite("node-0", "order#2 (lease still valid, sole leaseholder)");

phase("Phase 3: minority leader fails to renew, lease expires at t=1000ms");
cluster.run(600);
printEvents();
console.log(status());
tryWrite("node-0", "order#3");
tryWrite("node-1", "order#4");

phase("Phase 4: majority partition elects a new leader and resumes writes");
cluster.run(1500);
printEvents();
console.log(status());
const leader = cluster.leader();
console.log(`  serving leader: ${leader ? leader.id : "(none)"}`);
tryWrite(leader!.id, "order#5");
tryWrite("node-0", "order#6 (must stay rejected)");

phase("Phase 5: partition heals, cluster converges to one leader");
cluster.heal();
cluster.run(3000);
printEvents();
console.log(status());
console.log(`  serving leaders: [${cluster.servingLeaders().map((n) => n.id).join(", ")}]`);
console.log(
  `\nResult: ${cluster.servingLeaders().length === 1 ? "OK" : "FAIL"} — ` +
    "exactly one leader, minority partition never served writes after lease expiry.",
);
