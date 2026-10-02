import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/paxos/cluster.ts";
import { NOOP } from "../src/paxos/messages.ts";
import type { PaxosNode } from "../src/paxos/node.ts";

function committedValues(node: PaxosNode): string[] {
  const values: string[] = [];
  for (let slot = 1; slot <= node.commitIndex; slot++) {
    values.push(node.log.get(slot)!.value);
  }
  return values;
}

function assertNoHoles(node: PaxosNode): void {
  for (let slot = 1; slot <= node.commitIndex; slot++) {
    assert.ok(node.log.has(slot), `${node.id} has a hole at slot ${slot}`);
  }
}

test("leader election and basic replication (3 nodes)", () => {
  const cluster = new Cluster(["n1", "n2", "n3"]);
  const n1 = cluster.node("n1");
  n1.startElection();
  cluster.tick();

  assert.equal(n1.role, "leader");
  assert.equal(cluster.node("n2").promised.round, 1);
  assert.equal(cluster.node("n3").promised.round, 1);

  const slot = n1.propose("a");
  assert.equal(slot, 1);
  cluster.tick();

  for (const node of cluster.nodes.values()) {
    assert.equal(node.commitIndex, 1, `${node.id} commitIndex`);
    assert.equal(node.log.get(1)!.value, "a");
    assertNoHoles(node);
  }
  assert.equal(cluster.network.pending, 0, "no stranded messages");
  assert.equal(n1.pendingAccepts, 0, "leader acceptor state is pruned");
});

test("leader lease bypasses Phase 1 for consecutive entries", () => {
  const cluster = new Cluster(["n1", "n2", "n3"]);
  const n1 = cluster.node("n1");
  n1.startElection();
  cluster.tick();
  assert.equal(n1.role, "leader");

  cluster.network.resetCounts();
  n1.propose("x");
  n1.propose("y");
  n1.propose("z");
  cluster.tick();

  assert.equal(cluster.network.count("Prepare"), 0, "no Phase 1 under lease");
  assert.equal(cluster.network.count("Accept"), 6, "3 slots x 2 followers");
  assert.deepEqual(committedValues(n1), ["x", "y", "z"]);
  for (const node of cluster.nodes.values()) {
    assert.equal(node.commitIndex, 3);
    assertNoHoles(node);
  }
});

test("expired lease forces a new election before proposing", () => {
  const cluster = new Cluster(["n1", "n2", "n3"], 100);
  const n1 = cluster.node("n1");
  n1.startElection();
  cluster.tick();
  n1.propose("a");
  cluster.tick();

  cluster.advanceTime(200);
  const result = n1.propose("b");
  assert.equal(result, -1, "proposal deferred by lease expiry");
  assert.equal(n1.role, "candidate");

  cluster.tick();
  assert.equal(n1.role, "leader");
  assert.equal(n1.currentBallot.round, 2);

  n1.propose("b");
  cluster.tick();
  for (const node of cluster.nodes.values()) {
    assert.deepEqual(committedValues(node), ["a", "b"]);
  }
});

test("new leader repairs partially replicated slots via Phase 1", () => {
  const cluster = new Cluster(["n1", "n2", "n3"]);
  const n1 = cluster.node("n1");
  const n2 = cluster.node("n2");
  const n3 = cluster.node("n3");
  n1.startElection();
  cluster.tick();
  n1.propose("a");
  cluster.tick();
  assert.equal(n1.commitIndex, 1);

  // n1 "crashes" after slot 2 reached only n2 and slot 3 only n1 itself.
  const ballot = { round: 1, leaderId: "n1" };
  n2.receive({ type: "Accept", from: "n1", to: "n2", slot: 2, ballot, value: "x", commitIndex: 1 });
  n1.receive({ type: "Accept", from: "n1", to: "n1", slot: 3, ballot, value: "y", commitIndex: 1 });
  cluster.network.discard();

  n2.startElection();
  cluster.tick();
  assert.equal(n2.role, "leader");
  cluster.tick();

  for (const node of cluster.nodes.values()) {
    assert.deepEqual(committedValues(node), ["a", "x", "y"], `${node.id} log`);
    assert.equal(node.commitIndex, 3);
    assertNoHoles(node);
  }
  assert.equal(cluster.network.pending, 0);
});

test("unreplicated slot is filled with NOOP to keep the log gap-free", () => {
  const cluster = new Cluster(["n1", "n2", "n3"]);
  const n1 = cluster.node("n1");
  const n2 = cluster.node("n2");
  const n3 = cluster.node("n3");
  n1.startElection();
  cluster.tick();
  n1.propose("a");
  cluster.tick();

  // Slot 3 exists on the old leader, slot 2 exists nowhere: a hole.
  const ballot = { round: 1, leaderId: "n1" };
  n1.receive({ type: "Accept", from: "n1", to: "n1", slot: 3, ballot, value: "y", commitIndex: 1 });
  cluster.network.discard();

  n2.startElection();
  cluster.tick();
  cluster.tick();

  for (const node of cluster.nodes.values()) {
    assert.equal(node.log.get(2)!.value, NOOP, `${node.id} slot 2 is NOOP`);
    assert.equal(node.log.get(3)!.value, "y");
    assert.equal(node.commitIndex, 3);
    assertNoHoles(node);
  }
});

test("5-node partition: old leader deposed, minority dirty log safely overwritten", () => {
  const cluster = new Cluster(["n1", "n2", "n3", "n4", "n5"]);
  const n1 = cluster.node("n1");
  const n3 = cluster.node("n3");

  n1.startElection();
  cluster.tick();
  n1.propose("v1");
  cluster.tick();
  for (const node of cluster.nodes.values()) {
    assert.equal(node.commitIndex, 1);
  }

  // Partition: {n1,n2} minority | {n3,n4,n5} majority.
  cluster.network.partition([
    ["n1", "n2"],
    ["n3", "n4", "n5"],
  ]);

  // Old leader keeps writing in the minority partition: never commits.
  n1.propose("dirty-2");
  cluster.tick();
  n1.propose("dirty-3");
  cluster.tick();
  assert.equal(n1.commitIndex, 1, "minority leader cannot commit");
  assert.equal(cluster.node("n2").log.get(2)!.value, "dirty-2");
  assert.equal(cluster.node("n3").log.has(2), false);

  // Majority side elects a new leader; Phase 1 finds nothing past slot 1.
  cluster.network.resetCounts();
  n3.startElection();
  cluster.tick();
  assert.equal(n3.role, "leader");
  assert.ok(cluster.network.count("Prepare") > 0, "Phase 1 ran for election");

  n3.propose("v2");
  cluster.tick();
  assert.equal(n3.commitIndex, 2);
  assert.equal(cluster.node("n4").commitIndex, 2);
  assert.equal(cluster.node("n5").commitIndex, 2);
  assert.equal(n3.log.get(2)!.value, "v2");

  // Heal. Old leader (lease still valid on its clock) tries one more write;
  // the new leader continues in the majority.
  cluster.network.heal();
  n1.propose("stale-4");
  n3.propose("v3");
  cluster.tick();
  cluster.tick();

  for (const node of cluster.nodes.values()) {
    assert.deepEqual(
      committedValues(node),
      ["v1", "v2", "v3"],
      `${node.id} converged committed log`,
    );
    assert.equal(node.commitIndex, 3);
    assert.equal(node.log.has(4), false, `${node.id} dirty slot 4 removed`);
    assertNoHoles(node);
  }
  assert.equal(n1.role, "follower", "old leader stepped down");
  assert.equal(cluster.network.pending, 0, "no stranded messages");
  for (const node of cluster.nodes.values()) {
    assert.equal(node.pendingAccepts, 0, `${node.id} has no leaked acceptor state`);
  }
});

test("multiple in-flight proposals commit in slot order", () => {
  const cluster = new Cluster(["n1", "n2", "n3"]);
  const n1 = cluster.node("n1");
  n1.startElection();
  cluster.tick();

  const values = ["a", "b", "c", "d", "e"];
  for (const value of values) n1.propose(value);
  cluster.tick();

  for (const node of cluster.nodes.values()) {
    assert.deepEqual(committedValues(node), values);
    assert.equal(node.commitIndex, 5);
    assertNoHoles(node);
  }
  assert.equal(n1.pendingAccepts, 0);
  assert.equal(cluster.network.pending, 0);
});

test("stale leader Accept is rejected by majority that promised a higher ballot", () => {
  const cluster = new Cluster(["n1", "n2", "n3"]);
  const n1 = cluster.node("n1");
  const n2 = cluster.node("n2");
  n1.startElection();
  cluster.tick();
  n1.propose("a");
  cluster.tick();

  // n2 becomes leader at a higher ballot.
  n2.startElection();
  cluster.tick();
  assert.equal(n2.role, "leader");
  assert.equal(n1.role, "follower");

  // A forged stale Accept from the old ballot is ignored everywhere.
  const stale = { round: 1, leaderId: "n1" };
  for (const node of cluster.nodes.values()) {
    node.receive({ type: "Accept", from: "n1", to: node.id, slot: 2, ballot: stale, value: "evil", commitIndex: 1 });
  }
  cluster.tick();
  for (const node of cluster.nodes.values()) {
    assert.equal(node.log.has(2), false, `${node.id} ignored stale Accept`);
    assert.equal(node.commitIndex, 1);
  }
});
