import { ClusterNode, gossipUntilStable } from "./clusterNode.ts";

/**
 * Acceptance scenario:
 *  - 5-node cluster (A..E), each node accumulates tombstones at causal
 *    times T <= 25 while partitioned.
 *  - Nodes advance their clocks past T = 50, then gossip.
 *  - Once nodes A and B (and every peer) observe that ALL nodes have
 *    passed T = 50, the causal stability frontier covers every tombstone
 *    with T < 50 and the coordinator physically reclaims them
 *    automatically — memory per node drops by >= 90%.
 */

const CLUSTER = ["A", "B", "C", "D", "E"] as const;
const TOMBSTONES_PER_NODE = 400;
const DELETES_PER_TX = 16;
const PAYLOAD_SIZE = 256;
const WATERMARK = 50;

function fmt(bytes: number): string {
  return `${(bytes / 1024).toFixed(1)} KiB`;
}

const nodes = CLUSTER.map((id) => new ClusterNode(id, CLUSTER, PAYLOAD_SIZE));
const byId = new Map(nodes.map((n) => [n.id, n]));

for (const node of nodes) {
  node.coordinator.onGc((e) => {
    console.log(
      `  [GC @ ${node.id}] frontier=${JSON.stringify(e.frontier)} ` +
        `reclaimed ${e.removed} tombstones (${fmt(e.bytesFreed)})`,
    );
  });
}

// Phase 1 — partitioned: deletes create tombstones at causal times T <= 25.
for (const node of nodes) {
  const batches = TOMBSTONES_PER_NODE / DELETES_PER_TX;
  for (let b = 0; b < batches; b += 1) {
    node.transact(() => {
      for (let i = 0; i < DELETES_PER_TX; i += 1) {
        const key = `doc:${node.id}:${b}:${i}`;
        node.put(key, `value-${b}-${i}`);
        node.delete(key);
      }
      node.put(`live:${node.id}:${b}`, "still-alive");
    });
  }
}

// Phase 2 — still partitioned: nodes advance past T = 50 with live writes.
for (const node of nodes) {
  while (node.localTime() < 60) {
    node.transact(() => node.put(`live:${node.id}:t${node.localTime()}`, "x"));
  }
}

console.log("=== Phase 1+2: tombstones accumulated while partitioned ===");
const before = new Map<string, number>();
for (const node of nodes) {
  const s = node.store.stats();
  before.set(node.id, s.totalBytes);
  console.log(
    `  node ${node.id}: T=${node.localTime()} tombstones=${s.tombstones} ` +
      `live=${s.liveEntries} mem=${fmt(s.totalBytes)}`,
  );
}

// Phase 3 — gossip: every node learns every other node passed T = 50.
console.log("\n=== Phase 3: gossip exchanges causal clocks (auto-GC fires) ===");
const rounds = gossipUntilStable(nodes);
console.log(`  clocks quiesced after ${rounds} gossip round(s)`);

// Phase 4 — post-frontier deletes on A, not yet gossiped: must survive GC.
const nodeA = byId.get("A")!;
for (let i = 0; i < 5; i += 1) {
  nodeA.transact(() => {
    nodeA.put(`recent:A:${i}`, "fresh");
    nodeA.delete(`recent:A:${i}`);
  });
}

console.log("\n=== Phase 4: post-GC state ===");
const after = new Map<string, number>();
for (const node of nodes) {
  const s = node.store.stats();
  after.set(node.id, s.totalBytes);
  const frontier = node.coordinator.frontier();
  console.log(
    `  node ${node.id}: frontier=${frontier} tombstones=${s.tombstones} ` +
      `mem=${fmt(s.totalBytes)}`,
  );
}

// Acceptance checks.
console.log("\n=== Acceptance ===");
let ok = true;
for (const id of ["A", "B"]) {
  const frontier = byId.get(id)!.coordinator.frontier()!;
  const passed = frontier.minComponent() >= WATERMARK;
  ok &&= passed;
  console.log(
    `  node ${id} confirms all nodes passed T=${WATERMARK}: ` +
      `${passed ? "YES" : "NO"} (watermark=${frontier.minComponent()})`,
  );
}
let totalBefore = 0;
let totalAfter = 0;
for (const node of nodes) {
  totalBefore += before.get(node.id)!;
  totalAfter += after.get(node.id)!;
}
const drop = 1 - totalAfter / totalBefore;
ok &&= drop >= 0.9;
console.log(
  `  cluster memory: ${fmt(totalBefore)} -> ${fmt(totalAfter)} ` +
    `(drop ${(drop * 100).toFixed(1)}%, required >= 90%)`,
);
const remaining = nodes.reduce((n, node) => n + node.store.stats().tombstones, 0);
console.log(`  tombstones left cluster-wide: ${remaining} (only post-frontier ones)`);
console.log(ok ? "\nRESULT: PASS" : "\nRESULT: FAIL");
process.exit(ok ? 0 : 1);
