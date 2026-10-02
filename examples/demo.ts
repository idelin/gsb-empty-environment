import { Cluster } from "../src/paxos/cluster.ts";
import { ShardEngine } from "../src/sharding/engine.ts";
import type { PaxosNode } from "../src/paxos/node.ts";

function showLog(node: PaxosNode): string {
  const entries: string[] = [];
  for (let slot = 1; slot <= node.maxSlot; slot++) {
    const entry = node.log.get(slot);
    entries.push(entry ? `${slot}:${entry.value}` : `${slot}:_`);
  }
  return `${node.id.padEnd(3)} [${node.role.padEnd(8)}] commit=${node.commitIndex} log={ ${entries.join(", ")} }`;
}

function showCluster(cluster: Cluster, title: string): void {
  console.log(`\n--- ${title} ---`);
  for (const node of cluster.nodes.values()) console.log("  " + showLog(node));
}

function paxosDemo(): void {
  console.log("=== Multi-Paxos: 5-node cluster, partition, failover, dirty-log repair ===");
  const cluster = new Cluster(["n1", "n2", "n3", "n4", "n5"]);
  const n1 = cluster.node("n1");
  const n3 = cluster.node("n3");

  n1.startElection();
  cluster.tick();
  n1.propose("set x=1");
  cluster.tick();
  showCluster(cluster, "n1 elected (round 1), slot 1 committed via lease fast-path");

  cluster.network.partition([
    ["n1", "n2"],
    ["n3", "n4", "n5"],
  ]);
  n1.propose("set x=2 (dirty)");
  cluster.tick();
  showCluster(cluster, "partition {n1,n2}|{n3,n4,n5}: minority writes never commit");

  n3.startElection();
  cluster.tick();
  n3.propose("set y=9");
  cluster.tick();
  showCluster(cluster, "n3 elected in majority (round 2), Phase 1 repaired slots, slot 2 committed");

  cluster.network.heal();
  n3.heartbeat();
  cluster.tick();
  showCluster(cluster, "partition healed: dirty minority entries overwritten, logs converged");
}

function shardingDemo(): void {
  console.log("\n=== Hierarchical sharding + locality-aware routing ===");
  const engine = new ShardEngine(256, 3);
  for (const region of ["cn-east", "cn-north"]) {
    for (const zone of ["z1", "z2"]) {
      for (let i = 0; i < 2; i++) {
        engine.join({ id: `${region}-${zone}-${i}`, region, zone });
      }
    }
  }
  console.log(`members=${engine.memberCount} vshards=256 replicas=3 version=${engine.version}`);
  console.log(`invariants: ${engine.invariants().length === 0 ? "OK" : engine.invariants().join("; ")}`);

  const clients = [
    { region: "cn-east", zone: "z1" },
    { region: "cn-north", zone: "z2" },
  ];
  const histogram = new Map<number, number>();
  for (let i = 0; i < 10000; i++) {
    const decision = engine.route(`order-${i}`, clients[i % clients.length]);
    histogram.set(decision.distance, (histogram.get(decision.distance) ?? 0) + 1);
  }
  console.log("route distance histogram (1=same zone, 2=same region, 3=cross-region):");
  for (const [distance, count] of [...histogram].sort()) {
    console.log(`  distance ${distance}: ${count} (${((count / 10000) * 100).toFixed(1)}%)`);
  }

  const stats = engine.stats();
  const loads = [...stats.perNode.entries()].sort();
  console.log("per-node replica load:");
  for (const [id, count] of loads) console.log(`  ${id}: ${count}`);
}

paxosDemo();
shardingDemo();
