import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/cluster.ts";

/** Advance the cluster, asserting the core invariant after every tick. */
function runWithInvariant(cluster: Cluster, ms: number): void {
  const target = cluster.clock.now() + ms;
  while (cluster.clock.now() < target) {
    cluster.tick();
    const serving = cluster.servingLeaders();
    assert.ok(
      serving.length <= 1,
      `split-brain at t=${cluster.clock.now()}ms: ` +
        serving.map((n) => n.id).join(", "),
    );
  }
}

test("at most one serving leader exists at any moment (symmetric partition + heal)", () => {
  const cluster = new Cluster({ size: 5 });
  cluster.elect("node-0");
  runWithInvariant(cluster, 400);

  cluster.partition([
    ["node-0", "node-1"],
    ["node-2", "node-3", "node-4"],
  ]);
  runWithInvariant(cluster, 5000);

  cluster.heal();
  runWithInvariant(cluster, 3000);

  // After healing the cluster converges to exactly one leader.
  assert.equal(cluster.servingLeaders().length, 1);
});

test("asymmetric partition: leader that cannot confirm quorum loses the lease", () => {
  const cluster = new Cluster({ size: 5 });
  const leader = cluster.elect("node-0");
  runWithInvariant(cluster, 400);

  // One-way failure: everyone hears node-0's lease requests, but the grant
  // responses from node-2..4 never come back, so node-0 cannot confirm a
  // quorum even though the link looks half-alive.
  for (const id of ["node-2", "node-3", "node-4"]) {
    cluster.network.blockLink(id, "node-0");
  }

  // Lease granted at the last successful renewal cannot be extended.
  runWithInvariant(cluster, 1000);
  assert.equal(leader.state, "follower");
  assert.throws(() => leader.write("x"), /write rejected/);

  // A node with working quorum connectivity takes over; invariant holds
  // throughout. (node-1's links are intact, so it is a valid successor.)
  runWithInvariant(cluster, 4000);
  const successor = cluster.leader();
  assert.ok(successor);
  assert.notEqual(successor!.id, "node-0");
  assert.doesNotThrow(() => successor!.write("resumed"));
  assert.equal(cluster.servingLeaders().length, 1);
});

test("fencing tokens (terms) increase across leadership changes", () => {
  const cluster = new Cluster({ size: 5 });
  const first = cluster.elect("node-0");
  const firstAck = first.write("a");

  cluster.partition([
    ["node-0", "node-1"],
    ["node-2", "node-3", "node-4"],
  ]);
  runWithInvariant(cluster, 2500);

  const second = cluster.leader()!;
  const secondAck = second.write("b");
  assert.ok(secondAck.term > firstAck.term);
});
