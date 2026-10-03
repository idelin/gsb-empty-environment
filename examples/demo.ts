import { Cluster } from "../src/cluster.ts";

const NODE_IDS = ["A", "B", "C", "D", "E"];
const TOTAL_KEYS = 2000;
const OLD_TOMBSTONES = 1940;
const RECENT_TOMBSTONES = 10;
const PAYLOAD = "x".repeat(40);

const key = (i: number) => `doc-${String(i).padStart(6, "0")}`;

const cluster = new Cluster<string>(NODE_IDS);

console.log("=== Phase 1: load 2000 live objects across 5 nodes ===");
for (let i = 0; i < TOTAL_KEYS; i++) {
  cluster.replicatedSet(NODE_IDS[i % NODE_IDS.length], key(i), PAYLOAD);
}

console.log("=== Phase 2: delete 1940 of them at clocks T=10..49 (tombstones) ===");
for (let i = 0; i < OLD_TOMBSTONES; i++) {
  const origin = cluster.node(NODE_IDS[i % NODE_IDS.length]);
  origin.advanceTo(9 + (i % 40));
  cluster.replicatedDelete(origin.id, key(i));
}

console.log("=== Phase 3: every node advances past T=50, gossip to converge ===");
for (const node of cluster.nodes) node.advanceTo(51);
const rounds = cluster.gossipUntilConverged();
console.log(`gossip converged after ${rounds} round(s)`);

console.log("=== Phase 4: 10 fresh deletes at T>50 (must survive GC) ===");
for (let i = 0; i < RECENT_TOMBSTONES; i++) {
  const origin = cluster.node(NODE_IDS[i % NODE_IDS.length]);
  origin.advanceTo(60 + i);
  cluster.replicatedDelete(origin.id, key(OLD_TOMBSTONES + i));
}

console.log("\nCausal Stability Frontier as seen by A and B:");
for (const id of ["A", "B"]) {
  const frontier = cluster.node(id).frontier();
  const parts = NODE_IDS.map((n) => `${n}:${frontier.get(n)}`).join(" ");
  console.log(`  node ${id}: { ${parts} }`);
}

console.log("\n=== Phase 5: coordinated Tombstone GC on nodes A and B ===");
for (const id of ["A", "B"]) {
  const node = cluster.node(id);
  const before = node.store.bytes;
  const stats = node.runGc();
  const after = node.store.bytes;
  const reduction = ((1 - after / before) * 100).toFixed(1);
  console.log(
    `  node ${id}: scanned=${stats.scanned} reclaimed=${stats.reclaimed} ` +
      `bytes=${before} -> ${after} (-${reduction}%)`,
  );
}

console.log("\nOther nodes still hold their tombstones until they run the coordinator:");
for (const id of ["C", "D", "E"]) {
  const node = cluster.node(id);
  console.log(`  node ${id}: tombstones=${node.store.tombstones().length} bytes=${node.store.bytes}`);
}
