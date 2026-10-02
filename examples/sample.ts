import { Cluster, mulberry32 } from "../src/cluster.ts";

const CLUSTER_SIZE = 100;
const LOG2_PERIODS = Math.ceil(Math.log2(CLUSTER_SIZE));

const cluster = new Cluster(mulberry32(20261002));

const existing = Array.from(
  { length: CLUSTER_SIZE - 1 },
  (_, i) => `n${String(i + 1).padStart(3, "0")}`,
);
for (const id of existing) cluster.createNode(id);
for (const id of existing) {
  const node = cluster.nodes.get(id)!;
  for (const other of existing) {
    node.members.apply({ type: "join", nodeId: other, incarnation: 0 });
  }
}

console.log(`cluster ready: ${cluster.size - 1 + 1} slots, steady state seeded for ${existing.length} nodes`);
console.log(`max gossip rounds per event = lambda * ceil(log2(N)) = ${cluster.nodes.get("n001")!.gossip.roundsLimit}`);

const newcomer = cluster.createNode("n100");
cluster.join("n100", "n001");
console.log(`\nnode n100 joined via introducer n001 (informed: ${cluster.informedCount("n100")}/${cluster.size})`);

const samplePing = newcomer.makePing("n001");
console.log("\nsample ping packet with piggyback header:");
console.log(JSON.stringify(samplePing, null, 2));

console.log("\nperiod | informed nodes (of 100)");
console.log("-------+------------------------");
let convergedAt = -1;
for (let p = 1; p <= LOG2_PERIODS + 4; p += 1) {
  cluster.runPeriod();
  const informed = cluster.informedCount("n100");
  console.log(`${String(p).padStart(6)} | ${informed}`);
  if (informed === CLUSTER_SIZE) {
    convergedAt = p;
    break;
  }
}

console.log(
  `\nconverged in ${convergedAt} period(s); ceil(log2(100)) = ${LOG2_PERIODS}`,
);

for (let p = 0; p < newcomer.gossip.roundsLimit + 2; p += 1) cluster.runPeriod();
console.log(
  `after max-rounds limit: pending gossip events cluster-wide = ${cluster.pendingGossipTotal()}`,
);
