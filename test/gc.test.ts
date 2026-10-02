import { test } from "node:test";
import assert from "node:assert/strict";
import { VectorClock } from "../src/vectorClock.ts";
import { TombstoneStore } from "../src/tombstoneStore.ts";
import { TombstoneGcCoordinator } from "../src/coordinator.ts";
import { ClusterNode, gossipUntilStable } from "../src/clusterNode.ts";

const CLUSTER = ["A", "B", "C", "D", "E"] as const;

test("vector clock: tick, merge, entrywise min, dominates", () => {
  const a = new VectorClock();
  a.tick("A");
  a.tick("A");
  const b = VectorClock.from({ A: 1, B: 3 });
  a.merge(b);
  assert.equal(a.get("A"), 2);
  assert.equal(a.get("B"), 3);

  const min = VectorClock.entrywiseMin([
    VectorClock.from({ A: 5, B: 7 }),
    VectorClock.from({ A: 6, B: 4 }),
  ]);
  assert.deepEqual(min.snapshot(), { A: 5, B: 4 });

  assert.ok(VectorClock.from({ A: 5, B: 4 }).dominates(VectorClock.from({ A: 5, B: 4 })));
  assert.ok(VectorClock.from({ A: 9, B: 4 }).dominates(VectorClock.from({ A: 5, B: 3 })));
  assert.ok(!VectorClock.from({ A: 5, B: 3 }).dominates(VectorClock.from({ A: 5, B: 4 })));
});

test("store: deletes leave tombstones, prune frees tracked memory", () => {
  const store = new TombstoneStore(256);
  store.set("k1", "v1");
  store.set("k2", "v2");
  store.remove("k1", "A", { A: 3 });
  store.remove("k2", "A", { A: 4 });
  const before = store.stats();
  assert.equal(before.tombstones, 2);
  assert.ok(before.tombstoneBytes > 0);

  const result = store.prune((t) => (t.clock["A"] ?? 0) <= 3);
  assert.equal(result.removed, 1);
  assert.ok(result.bytesFreed > 0);
  const after = store.stats();
  assert.equal(after.tombstones, 1);
  assert.equal(after.tombstoneBytes, before.tombstoneBytes - result.bytesFreed);
  assert.ok(store.isDeleted("k2"));
  assert.ok(!store.isDeleted("k1"));
});

test("coordinator: no frontier until every node reported", () => {
  const store = new TombstoneStore(64);
  const gc = new TombstoneGcCoordinator("A", CLUSTER, store);
  gc.observe("A", { A: 10 });
  assert.equal(gc.frontier(), null);
  for (const id of ["B", "C", "D"]) gc.observe(id, { [id]: 10 });
  assert.equal(gc.frontier(), null);
  gc.observe("E", { E: 10 });
  assert.notEqual(gc.frontier(), null);
});

test("coordinator: tombstone stable only when frontier dominates its clock", () => {
  const store = new TombstoneStore(64);
  const gc = new TombstoneGcCoordinator("A", CLUSTER, store);
  const tombstoneClock = { A: 30 };
  for (const id of CLUSTER) gc.observe(id, { A: 29 });
  assert.equal(gc.isStable(tombstoneClock), false);
  // Only some nodes advance past 30: the frontier is the minimum, so the
  // tombstone stays unstable until the laggard catches up.
  for (const id of ["A", "B", "C", "D"]) gc.observe(id, { A: 31 });
  assert.equal(gc.isStable(tombstoneClock), false);
  gc.observe("E", { A: 30 });
  assert.equal(gc.isStable(tombstoneClock), true);
});

test("coordinator: rejects clocks from unknown nodes", () => {
  const gc = new TombstoneGcCoordinator("A", CLUSTER, new TombstoneStore());
  assert.throws(() => gc.observe("Z", { Z: 1 }), /unknown node/);
});

test("acceptance: 5-node cluster reclaims all T<50 tombstones, memory drops >=90%", () => {
  const nodes = CLUSTER.map((id) => new ClusterNode(id, CLUSTER, 256));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const gcEvents: string[] = [];
  for (const node of nodes) {
    node.coordinator.onGc((e) => gcEvents.push(`${node.id}:${e.removed}`));
  }

  // Partitioned phase: 400 tombstones per node at causal times T <= 25.
  for (const node of nodes) {
    for (let b = 0; b < 25; b += 1) {
      node.transact(() => {
        for (let i = 0; i < 16; i += 1) {
          const key = `doc:${node.id}:${b}:${i}`;
          node.put(key, "payload");
          node.delete(key);
        }
      });
    }
    while (node.localTime() < 60) {
      node.transact(() => node.put(`live:${node.id}:${node.localTime()}`, "x"));
    }
  }
  for (const node of nodes) {
    assert.equal(node.store.stats().tombstones, 400);
    assert.ok(node.localTime() >= 50);
  }
  const before = new Map(nodes.map((n) => [n.id, n.store.stats().totalBytes]));

  // Gossip: every node confirms every peer passed T=50; GC must be automatic.
  gossipUntilStable(nodes);
  for (const id of ["A", "B"]) {
    const frontier = byId.get(id)!.coordinator.frontier();
    assert.ok(frontier, `node ${id} has a frontier`);
    assert.ok(
      frontier!.minComponent() >= 50,
      `node ${id} confirms all nodes passed T=50 (watermark=${frontier!.minComponent()})`,
    );
  }
  assert.ok(gcEvents.length > 0, "GC ran automatically on gossip");

  // Every tombstone with T < 50 is physically gone on every node.
  for (const node of nodes) {
    assert.equal(node.store.stats().tombstones, 0, `node ${node.id} reclaimed all tombstones`);
    const drop = 1 - node.store.stats().totalBytes / before.get(node.id)!;
    assert.ok(drop >= 0.9, `node ${node.id} memory drop ${(drop * 100).toFixed(1)}% >= 90%`);
  }
});

test("safety: un-gossiped post-frontier tombstones survive GC", () => {
  const nodes = CLUSTER.map((id) => new ClusterNode(id, CLUSTER, 256));
  const nodeA = nodes[0];
  for (const node of nodes) {
    for (let b = 0; b < 5; b += 1) {
      node.transact(() => {
        node.put(`old:${node.id}:${b}`, "x");
        node.delete(`old:${node.id}:${b}`);
      });
    }
    while (node.localTime() < 60) node.transact(() => node.put(`l:${node.id}:${node.localTime()}`, "x"));
  }
  gossipUntilStable(nodes);
  assert.equal(nodeA.store.stats().tombstones, 0);

  // Fresh deletes on A after the frontier, not yet gossiped anywhere.
  for (let i = 0; i < 3; i += 1) {
    nodeA.transact(() => {
      nodeA.put(`recent:${i}`, "x");
      nodeA.delete(`recent:${i}`);
    });
  }
  assert.equal(nodeA.store.stats().tombstones, 3, "recent tombstones retained");
  assert.ok(nodeA.store.isDeleted("recent:0"));
});

test("safety: partitioned node blocks the frontier (no premature GC)", () => {
  const nodes = CLUSTER.map((id) => new ClusterNode(id, CLUSTER, 256));
  const [a, b, c, d, e] = nodes;
  for (const node of [a, b, c, d]) {
    node.transact(() => {
      node.put(`k:${node.id}`, "x");
      node.delete(`k:${node.id}`);
    });
    while (node.localTime() < 60) node.transact(() => node.put(`l:${node.localTime()}`, "x"));
  }
  // E is partitioned away: only A..D gossip among themselves.
  gossipUntilStable([a, b, c, d]);
  for (const node of [a, b, c, d]) {
    assert.equal(node.coordinator.frontier(), null, "no frontier without E");
    assert.ok(node.store.stats().tombstones > 0, "tombstones retained during partition");
  }
  // E heals and gossips: frontier forms, GC fires.
  while (e.localTime() < 60) e.transact(() => e.put(`l:e:${e.localTime()}`, "x"));
  gossipUntilStable(nodes);
  assert.ok(a.coordinator.frontier()!.minComponent() >= 50);
  assert.equal(a.store.stats().tombstones, 0);
});
