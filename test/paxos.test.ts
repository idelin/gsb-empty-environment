import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/cluster.ts";
import { NOOP_VALUE } from "../src/types.ts";
import { makeLogStore } from "../src/log-store.ts";
import type { Message } from "../src/network.ts";

/** Count messages per type by wrapping the network send. */
function countMessages(cluster: Cluster): Map<string, number> {
  const counts = new Map<string, number>();
  const orig = cluster.net.send.bind(cluster.net);
  cluster.net.send = (msg: Message) => {
    counts.set(msg.type, (counts.get(msg.type) ?? 0) + 1);
    orig(msg);
  };
  return counts;
}

// ------------------------------------------------------------ log store

test("LogStore: accept/get/commit and hole-stop semantics", () => {
  const { store, pool } = makeLogStore("t");
  const ballot = { round: 1, nodeId: 0 };
  store.accept(0, ballot, "a");
  store.accept(2, ballot, "c"); // hole at slot 1
  const applied: string[] = [];
  const idx = store.commitUpTo(2, (e) => applied.push(e.value));
  assert.equal(idx, 0, "commit stops at the hole");
  assert.deepEqual(applied, ["a"]);
  store.accept(1, ballot, "b");
  const idx2 = store.commitUpTo(2, (e) => applied.push(e.value));
  assert.equal(idx2, 2);
  assert.deepEqual(applied, ["a", "b", "c"]);
  store.dispose();
  assert.equal(pool.outstanding, 0);
});

test("LogStore: dirty overwrite reuses the same pooled instance (zero alloc)", () => {
  const { store, pool } = makeLogStore("t");
  const e1 = store.get(0);
  store.accept(0, { round: 1, nodeId: 0 }, "dirty");
  const before = pool.totalCreated;
  const entry = store.get(0);
  const overwritten = store.accept(0, { round: 2, nodeId: 1 }, "chosen");
  assert.equal(overwritten, true);
  assert.equal(store.get(0), entry, "same instance reused in place");
  assert.equal(store.get(0)?.value, "chosen");
  assert.equal(pool.totalCreated, before, "no new allocation on repair path");
  store.dispose();
  assert.equal(pool.outstanding, 0);
});

test("LogStore: committed value can never be contradicted", () => {
  const { store, pool } = makeLogStore("t");
  const ballot = { round: 1, nodeId: 0 };
  store.accept(0, ballot, "chosen");
  store.commitUpTo(0, () => {});
  assert.throws(() => store.accept(0, { round: 2, nodeId: 1 }, "different"), /contradicted/);
  // Idempotent re-accept of the chosen value is fine and keeps it committed.
  store.accept(0, { round: 2, nodeId: 1 }, "chosen");
  assert.equal(store.get(0)?.committed, true);
  store.dispose();
  assert.equal(pool.outstanding, 0);
});

test("LogStore: truncateFrom releases entries back to the pool", () => {
  const { store, pool } = makeLogStore("t");
  const ballot = { round: 1, nodeId: 0 };
  for (let s = 0; s < 10; s++) store.accept(s, ballot, `v${s}`);
  assert.equal(pool.outstanding, 10);
  store.truncateFrom(6);
  assert.equal(pool.outstanding, 6, "slots 0..5 remain checked out");
  assert.equal(store.maxAcceptedSlot(), 5);
  store.dispose();
  assert.equal(pool.outstanding, 0);
});

// ------------------------------------------------------- election & lease

test("leader lease enables Phase-1-free commits on the hot path", () => {
  const cluster = new Cluster(5, 1000);
  const counts = countMessages(cluster);
  cluster.elect(0);
  const preparesAfterElection = counts.get("prepare") ?? 0;
  for (let i = 0; i < 20; i++) cluster.propose(0, `cmd-${i}`);
  assert.equal(counts.get("prepare") ?? 0, preparesAfterElection, "no Prepare on the lease fast path");
  for (const node of cluster.nodes) {
    assert.equal(node.commitIndex, 19);
    assert.equal(node.appliedValues().length, 20);
  }
  cluster.dispose();
  assert.equal(cluster.outstandingEntries(), 0);
});

test("lease expiry forces the leader to step down and re-run Phase 1", () => {
  const cluster = new Cluster(5, 1000);
  const counts = countMessages(cluster);
  cluster.elect(0);
  cluster.propose(0, "a");
  cluster.advance(1500); // lease expires
  assert.ok(!cluster.nodes[0].isLeader, "leader stepped down on lease expiry");
  const before = counts.get("prepare") ?? 0;
  cluster.propose(0, "b"); // triggers re-election
  assert.ok((counts.get("prepare") ?? 0) > before, "Phase 1 re-run after lease loss");
  assert.equal(cluster.nodes[0].commitIndex, 1);
  cluster.dispose();
});

test("acceptors reject foreign Prepare while the lease is active", () => {
  const cluster = new Cluster(5, 1000);
  cluster.elect(0);
  cluster.nodes[3].becomeLeader();
  cluster.flush();
  assert.ok(!cluster.nodes[3].isLeader, "cannot usurp an active lease");
  assert.ok(cluster.nodes[0].isLeader, "original leader keeps leadership");
  cluster.advance(1500);
  cluster.elect(3); // after expiry the election succeeds
  cluster.dispose();
});

// ------------------------------------------- acceptance: partition scenario

test("5-node partition: new majority leader repairs holes and overwrites dirty minority log", () => {
  const cluster = new Cluster(5, 1000);
  const net = cluster.net;
  const leader0 = cluster.elect(0);

  cluster.propose(0, "set x=1"); // slot 0, committed everywhere
  cluster.propose(0, "set y=2"); // slot 1, committed everywhere

  // slot 2 ("dirty") reaches only the future minority {0,1}.
  cluster.partition([0, 1], [2, 3, 4]);
  leader0.propose("dirty");
  cluster.flush();

  // slot 3 ("set z=3") reaches a majority {0,1,2} but its commit is lost.
  cluster.heal();
  leader0.propose("set z=3");
  net.flushN(10);   // Accepts + Accepteds delivered; commit broadcast still queued
  net.dropPending(); // in-flight commits die with the re-partition
  cluster.partition([0, 1], [2, 3, 4]);

  // The isolated old leader cannot commit anything.
  leader0.propose("lost-write");
  cluster.flush();
  assert.equal(cluster.nodes[0].commitIndex, 1, "minority leader cannot advance commit");
  assert.equal(cluster.nodes[0].entry(2)?.value, "dirty", "dirty entry sits in the minority");

  // Lease expires; the majority partition elects a new leader.
  cluster.advance(1500);
  const leader2 = cluster.elect(2);
  assert.equal(leader2.commitIndex, 3, "Phase 1 repaired and committed slots 2..3");
  assert.deepEqual(leader2.appliedValues(), ["set x=1", "set y=2", NOOP_VALUE, "set z=3"]);

  // Heal: higher-ballot Accepts safely overwrite the dirty minority log.
  cluster.heal();
  leader2.syncFollowers();
  cluster.flush();
  cluster.propose(2, "set w=4"); // slot 4

  const expected = ["set x=1", "set y=2", NOOP_VALUE, "set z=3", "set w=4"];
  for (let i = 0; i < 5; i++) {
    assert.deepEqual(
      cluster.nodes[i].appliedValues(),
      expected,
      `node ${i} must converge to the majority log`
    );
    assert.equal(cluster.nodes[i].commitIndex, 4);
  }
  assert.equal(cluster.nodes[0].entry(2)?.value, NOOP_VALUE, "dirty slot overwritten, not committed");

  // Strict no-leak: disposing the cluster returns every pooled entry.
  cluster.dispose();
  assert.equal(cluster.outstandingEntries(), 0);
});

test("old leader's late Accepts are rejected by the new majority (ballot check)", () => {
  const cluster = new Cluster(5, 1000);
  const leader0 = cluster.elect(0);
  cluster.propose(0, "a");
  cluster.partition([0, 1], [2, 3, 4]);
  cluster.advance(1500);
  const leader2 = cluster.elect(2);
  cluster.propose(2, "b"); // slot 1 in the majority partition

  // Old leader (deposed but not yet aware) tries to run an election and
  // get "c" committed from the minority side.
  leader0.propose("c");
  cluster.flush();
  cluster.heal();
  cluster.advance(2000); // every outstanding lease expires
  cluster.flush();       // stale ballots cross the healed link, rejected
  cluster.propose(2, "d"); // node 2 re-wins Phase 1, then commits

  const logs = cluster.appliedLogs();
  for (const l of logs) assert.deepEqual(l, ["a", "b", "d"]);
  assert.ok(!logs[0].includes("c"), "stale leader's write never committed");
  cluster.dispose();
  assert.equal(cluster.outstandingEntries(), 0);
});

test("repair overwrite path allocates no new LogEntry objects", () => {
  const cluster = new Cluster(5, 1000);
  const leader0 = cluster.elect(0);
  cluster.propose(0, "a"); // slot 0 committed
  cluster.partition([0, 1], [2, 3, 4]);
  leader0.propose("dirty-1"); // slot 1 on minority {0,1}
  leader0.propose("dirty-2"); // slot 2 on minority {0,1}
  cluster.flush();
  cluster.advance(1500);
  const leader2 = cluster.elect(2); // majority {2,3,4}; nextSlot = 1
  cluster.heal();

  // The new leader's fresh proposals reuse slots 1..2; on nodes 0,1 they
  // OVERWRITE the dirty entries in place — no new pooled objects there.
  const createdBefore = [cluster.pools[0].totalCreated, cluster.pools[1].totalCreated];
  cluster.propose(2, "b"); // slot 1
  cluster.propose(2, "c"); // slot 2
  const createdAfter = [cluster.pools[0].totalCreated, cluster.pools[1].totalCreated];
  assert.deepEqual(createdAfter, createdBefore, "in-place reuse: zero new entries during repair");
  assert.equal(cluster.nodes[0].entry(1)?.value, "b");
  assert.equal(cluster.nodes[1].entry(2)?.value, "c");
  for (const l of cluster.appliedLogs()) assert.deepEqual(l, ["a", "b", "c"]);
  cluster.dispose();
  assert.equal(cluster.outstandingEntries(), 0);
});

test("concurrent client proposals keep the log contiguous and identical", () => {
  const cluster = new Cluster(5, 1000);
  cluster.elect(0);
  const values = Array.from({ length: 100 }, (_, i) => `op-${i}`);
  for (const v of values) cluster.nodes[0].propose(v);
  cluster.flush();
  for (const node of cluster.nodes) {
    assert.deepEqual(node.appliedValues(), values);
    assert.equal(node.commitIndex, 99);
  }
  cluster.dispose();
  assert.equal(cluster.outstandingEntries(), 0);
});
