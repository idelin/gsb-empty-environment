import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/cluster.ts";

/**
 * Acceptance boundary: a 5-node cluster is split into a 2-node and a 3-node
 * partition. The 3-node partition keeps renewing leases and serving writes;
 * the 2-node partition cannot reach quorum, so its lease expires and the old
 * leader is automatically demoted — no split-brain.
 */
test("5-node cluster, 2/3 partition: majority serves, minority loses the lease", () => {
  const cluster = new Cluster({ size: 5 });
  const oldLeader = cluster.elect("node-0");
  cluster.run(400);

  // Healthy cluster: leader serves writes.
  assert.doesNotThrow(() => oldLeader.write("before-partition"));

  // Symmetric network partition: {node-0, node-1} | {node-2, node-3, node-4}.
  cluster.partition([
    ["node-0", "node-1"],
    ["node-2", "node-3", "node-4"],
  ]);

  // The old leader can still serve writes until its lease expires at t=1000;
  // it is still the only leaseholder, so this is safe.
  assert.doesNotThrow(() => oldLeader.write("grace-window"));

  // t=1000: renewals reached only 2 of 5 nodes (< quorum 3), lease expires.
  cluster.run(600);
  assert.equal(oldLeader.state, "follower", "minority leader must step down");
  assert.equal(oldLeader.lease(), null);
  assert.throws(() => oldLeader.write("after-expiry"), /write rejected/);
  assert.throws(() => cluster.node("node-1").write("minority"), /write rejected/);

  // Brief window with no leader at all: availability is sacrificed, never safety.
  assert.equal(cluster.leader(), undefined);

  // t=1700: the majority partition elects a new leader (quorum of 3).
  cluster.run(1000);
  const newLeader = cluster.leader();
  assert.ok(newLeader, "majority partition must elect a leader");
  assert.ok(["node-2", "node-3", "node-4"].includes(newLeader!.id));
  assert.ok(newLeader!.term > oldLeader.term, "new term must be higher");

  // Majority keeps serving and renewing.
  const ack = newLeader!.write("majority-write");
  assert.equal(ack.term, newLeader!.term);
  cluster.run(1500);
  assert.equal(cluster.leader()?.id, newLeader!.id);
  assert.doesNotThrow(() => newLeader!.write("still-serving"));

  // Minority side never regains leadership while partitioned.
  assert.notEqual(cluster.node("node-0").state, "leader");
  assert.notEqual(cluster.node("node-1").state, "leader");
  assert.throws(() => cluster.node("node-0").write("nope"), /write rejected/);
});

test("minority partition stays read-only for the whole partition duration", () => {
  const cluster = new Cluster({ size: 5 });
  cluster.elect("node-0");
  cluster.partition([
    ["node-0", "node-1"],
    ["node-2", "node-3", "node-4"],
  ]);

  // Run 10s of simulated time. The old leader may finish its grace window
  // (lease granted at t=0, expires at t=1000); after that the minority must
  // never serve again, and node-1 must never serve at all.
  for (let t = 0; t < 10_000; t += 50) {
    cluster.tick();
    assert.equal(cluster.node("node-1").isServingLeader(), false);
    if (cluster.clock.now() >= 1000) {
      assert.equal(
        cluster.node("node-0").isServingLeader(),
        false,
        `node-0 must stop serving after lease expiry (t=${cluster.clock.now()})`,
      );
    }
  }
  assert.ok(cluster.leader(), "majority side keeps a leader");
});
