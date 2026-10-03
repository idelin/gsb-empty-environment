import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/cluster.ts";
import { ManualClock } from "../src/clock.ts";
import { Network } from "../src/network.ts";
import { Node } from "../src/node.ts";
import type { Message } from "../src/types.ts";

test("quorum is a strict majority of the cluster", () => {
  assert.equal(new Cluster({ size: 5 }).quorum, 3);
  assert.equal(new Cluster({ size: 4 }).quorum, 3);
  assert.equal(new Cluster({ size: 3 }).quorum, 2);
});

test("candidate with quorum grants becomes leader and serves writes", () => {
  const cluster = new Cluster({ size: 5 });
  const leader = cluster.elect("node-0");

  assert.equal(leader.state, "leader");
  assert.equal(leader.term, 1);
  assert.equal(leader.lease()?.expiresAt, 1000);
  assert.equal(cluster.leader()?.id, "node-0");

  const ack = leader.write("first");
  assert.deepEqual(ack, { term: 1, index: 0, value: "first" });
});

test("candidate in a minority partition cannot collect quorum", () => {
  const cluster = new Cluster({ size: 5 });
  cluster.partition([
    ["node-0", "node-1"],
    ["node-2", "node-3", "node-4"],
  ]);

  const minority = cluster.node("node-0");
  minority.startElection();
  assert.notEqual(minority.state, "leader");
  assert.equal(minority.isServingLeader(), false);

  const majority = cluster.node("node-2");
  majority.startElection();
  assert.equal(majority.state, "leader");
  assert.equal(cluster.leader()?.id, "node-2");
});

test("a voter never grants to a second holder while its lease is unexpired", () => {
  const clock = new ManualClock();
  const network = new Network();
  const replies: Message[] = [];
  const collector = { receive: (m: Message) => replies.push(m) };
  network.register("node-0", collector);
  network.register("node-2", collector);

  const voter = new Node(clock, network, {
    id: "node-1",
    clusterSize: 3,
    leaseTtlMs: 1000,
    electionTimeoutMs: 60_000,
    renewBeforeMs: 500,
  });
  network.register("node-1", voter);

  voter.receive({ type: "LeaseRequest", from: "node-0", term: 1 });
  assert.equal(voter.granted()?.holderId, "node-0");

  // Higher term does NOT override an unexpired grant to another holder.
  voter.receive({ type: "LeaseRequest", from: "node-2", term: 2 });
  assert.equal(voter.granted()?.holderId, "node-0");
  assert.deepEqual(replies.at(-1), {
    type: "LeaseResponse",
    from: "node-1",
    term: 2,
    granted: false,
    reason: "lease-held",
    expiresAt: undefined,
  });

  // After the lease expires the voter is free to grant again.
  clock.advance(1000);
  voter.receive({ type: "LeaseRequest", from: "node-2", term: 2 });
  assert.equal(voter.granted()?.holderId, "node-2");
  assert.equal((replies.at(-1) as { granted: boolean }).granted, true);
});

test("leader renews its lease while it can reach a quorum", () => {
  const cluster = new Cluster({ size: 5 });
  const leader = cluster.elect("node-0");

  cluster.run(500); // renewal window opens at t=500
  assert.ok(leader.lease()!.expiresAt > 1000, "lease should be extended");

  cluster.run(2000);
  assert.equal(cluster.leader()?.id, "node-0");
  assert.ok(leader.lease()!.expiresAt > cluster.clock.now());
});

test("leader without quorum cannot extend its lease and loses writes at expiry", () => {
  const cluster = new Cluster({ size: 5 });
  const leader = cluster.elect("node-0");
  cluster.partition([
    ["node-0", "node-1"],
    ["node-2", "node-3", "node-4"],
  ]);

  cluster.run(1000); // renewals fail; lease granted at t=0 expires at t=1000
  assert.equal(leader.state, "follower");
  assert.throws(() => leader.write("lost"), /write rejected/);
});
