import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { Cluster } from "../src/cluster.ts";
import { ClockMatrix } from "../src/clockMatrix.ts";

const NODE_IDS = ["A", "B", "C", "D", "E"];
const TOTAL_KEYS = 2000;
const OLD_TOMBSTONES = 1940;
const RECENT_TOMBSTONES = 10;
const PAYLOAD = "x".repeat(40);

function key(i: number): string {
  return `doc-${String(i).padStart(6, "0")}`;
}

function buildCluster(): Cluster<string> {
  const cluster = new Cluster<string>(NODE_IDS);
  for (let i = 0; i < TOTAL_KEYS; i++) {
    cluster.replicatedSet(NODE_IDS[i % NODE_IDS.length], key(i), PAYLOAD);
  }
  for (let i = 0; i < OLD_TOMBSTONES; i++) {
    const origin = cluster.node(NODE_IDS[i % NODE_IDS.length]);
    origin.advanceTo(9 + (i % 40));
    cluster.replicatedDelete(origin.id, key(i));
  }
  return cluster;
}

describe("ClockMatrix causal stability frontier", () => {
  it("computes the component-wise minimum across all known node vectors", () => {
    const matrix = new ClockMatrix();
    matrix.observe("A", { A: 50, B: 60, C: 70 });
    matrix.observe("B", { A: 55, B: 60, C: 40 });
    matrix.observe("C", { A: 52, B: 61, C: 70 });
    const frontier = matrix.frontier();
    assert.equal(frontier.get("A"), 50);
    assert.equal(frontier.get("B"), 60);
    assert.equal(frontier.get("C"), 40);
  });
});

describe("TombstoneGcCoordinator acceptance: 5-node cluster, frontier T=50", () => {
  it("physically reclaims every tombstone with T<50 on A and B, dropping memory >=90%", () => {
    const cluster = buildCluster();

    for (const node of cluster.nodes) node.advanceTo(51);
    const rounds = cluster.gossipUntilConverged();
    assert.ok(rounds >= 1, "at least one gossip round happened");

    for (const id of ["A", "B"]) {
      const frontier = cluster.node(id).frontier();
      for (const peer of NODE_IDS) {
        assert.ok(
          (frontier.get(peer) ?? 0) > 50,
          `node ${id} sees peer ${peer} past T=50 (got ${frontier.get(peer)})`,
        );
      }
    }

    for (let i = 0; i < RECENT_TOMBSTONES; i++) {
      const origin = cluster.node(NODE_IDS[i % NODE_IDS.length]);
      origin.advanceTo(60 + i);
      cluster.replicatedDelete(origin.id, key(OLD_TOMBSTONES + i));
    }

    for (const id of ["A", "B"]) {
      const node = cluster.node(id);
      const bytesBefore = node.store.bytes;
      const tombstonesBefore = node.store.tombstones().length;
      assert.equal(tombstonesBefore, OLD_TOMBSTONES + RECENT_TOMBSTONES);

      const stats = node.runGc();

      assert.equal(stats.scanned, OLD_TOMBSTONES + RECENT_TOMBSTONES);
      assert.equal(stats.reclaimed, OLD_TOMBSTONES);
      assert.equal(node.store.tombstones().length, RECENT_TOMBSTONES);
      for (const t of node.store.tombstones()) {
        assert.ok(t.deletedAt && t.deletedAt.clock > 50, "only T>=50 tombstones survive");
      }

      const bytesAfter = node.store.bytes;
      const reduction = 1 - bytesAfter / bytesBefore;
      console.log(
        `node ${id}: bytes ${bytesBefore} -> ${bytesAfter} ` +
          `(-${(reduction * 100).toFixed(1)}%), reclaimed ${stats.reclaimed} tombstones`,
      );
      assert.ok(reduction >= 0.9, `memory reduction ${(reduction * 100).toFixed(1)}% >= 90%`);
    }
  });

  it("reclaims nothing when a lagging node has not passed T=50 (safety)", () => {
    const cluster = buildCluster();
    for (const node of cluster.nodes) node.advanceTo(51);
    cluster.gossipRound(["E"]);
    const staleVector = { A: 0, B: 0, C: 0, D: 0, E: 0 };
    for (const node of cluster.nodes) {
      if (node.id !== "E") node.receiveGossip("E", staleVector);
    }

    const stats = cluster.node("A").runGc();
    assert.equal(stats.reclaimed, 0);
    assert.equal(cluster.node("A").store.tombstones().length, OLD_TOMBSTONES);
  });

  it("is idempotent: a second GC pass reclaims nothing", () => {
    const cluster = buildCluster();
    for (const node of cluster.nodes) node.advanceTo(51);
    cluster.gossipUntilConverged();

    const node = cluster.node("A");
    const first = node.runGc();
    assert.equal(first.reclaimed, OLD_TOMBSTONES);
    const second = node.runGc();
    assert.equal(second.reclaimed, 0);
    assert.equal(second.reclaimedBytes, 0);
  });
});
