import { test } from "node:test";
import assert from "node:assert/strict";
import { Cluster } from "../src/sim.ts";
import { RaftNode } from "../src/raft.ts";
import type { LogEntry, Message, NodeId } from "../src/raft.ts";

const commands = (log: LogEntry[]) => log.map((e) => e.command);

/** Deterministic 3-node cluster. C times out first, then A, then B. */
function makeCluster(): Cluster {
  return new Cluster([
    { id: "C", electionTimeout: 10, heartbeatInterval: 3 },
    { id: "A", electionTimeout: 15, heartbeatInterval: 3 },
    { id: "B", electionTimeout: 20, heartbeatInterval: 3 },
  ]);
}

/** Node wired to a captured outbox, for message-level unit tests. */
function makeNode(log: LogEntry[] = [], commitIndex = 0) {
  const outbox: { to: NodeId; msg: Message }[] = [];
  const node = new RaftNode({
    id: "N",
    peers: ["P1", "P2"],
    electionTimeout: 10,
    heartbeatInterval: 3,
    send: (to, msg) => outbox.push({ to, msg }),
  });
  node.log.push(...log);
  node.commitIndex = commitIndex;
  return { node, outbox };
}

test("acceptance: view change, partition progress, stale-leader rollback", () => {
  const cluster = makeCluster();
  const [a, b, c] = [cluster.node("A"), cluster.node("B"), cluster.node("C")];

  // ---- Phase 1: 3-node cluster commits 3 entries in term 1 -----------------
  cluster.tick(10); // C's election timeout fires: pre-vote -> election -> leader
  assert.equal(c.role, "leader");
  assert.equal(c.currentTerm, 1);

  c.propose("e1");
  c.propose("e2");
  c.propose("e3");
  cluster.flush();

  for (const n of [a, b, c]) {
    assert.equal(n.commitIndex, 3, `${n.id} commits 3 entries`);
    assert.deepEqual(commands(n.log), ["e1", "e2", "e3"]);
    assert.ok(n.log.every((e) => e.term === 1));
  }

  // ---- Phase 2: isolate C (the term-1 leader); A/B advance to term 2 ------
  cluster.isolate("C");

  // Stale leader C still accepts proposals but cannot commit without quorum.
  c.propose("y4");
  c.propose("y5");
  cluster.flush();
  assert.equal(c.role, "leader");
  assert.equal(c.currentTerm, 1);
  assert.equal(c.commitIndex, 3, "isolated leader cannot commit");
  assert.deepEqual(commands(c.log), ["e1", "e2", "e3", "y4", "y5"]);

  // Test clock drives A/B to a new election. A's pre-vote at t=15 is rejected
  // by B (leader lease); B's pre-vote at t=20 is granted -> term 2.
  cluster.tick(20);
  assert.equal(b.role, "leader");
  assert.equal(b.currentTerm, 2);
  assert.equal(a.currentTerm, 2);

  // Majority partition A/B commits 4 more entries in term 2.
  b.propose("e4");
  b.propose("e5");
  b.propose("e6");
  b.propose("e7");
  cluster.flush();
  for (const n of [a, b]) {
    assert.equal(n.commitIndex, 7, `${n.id} commits 7 entries`);
    assert.deepEqual(commands(n.log), ["e1", "e2", "e3", "e4", "e5", "e6", "e7"]);
  }
  assert.deepEqual(commands(c.log), ["e1", "e2", "e3", "y4", "y5"], "C untouched while isolated");

  // ---- Phase 3: heal C; high-term heartbeat forces rollback ----------------
  cluster.healAll();
  cluster.tick(3); // B's heartbeat interval elapses: AppendEntries(term=2) -> C

  assert.equal(c.role, "follower", "stale leader steps down on higher term");
  assert.equal(c.currentTerm, 2, "term moves monotonically forward");
  assert.equal(c.commitIndex, 7, "C adopts the committed prefix");
  assert.deepEqual(commands(c.log), ["e1", "e2", "e3", "e4", "e5", "e6", "e7"],
    "conflicting uncommitted entries y4/y5 are overwritten");
  assert.deepEqual(c.log.slice(0, 3).map((e) => e.command), ["e1", "e2", "e3"],
    "committed entries are never lost");
  assert.deepEqual(c.log, b.log, "C converges to the leader's log");

  // Cluster remains stable: C's pre-votes cannot disrupt the healthy leader.
  cluster.tick(50);
  assert.equal(b.role, "leader");
  assert.equal(b.currentTerm, 2);
  assert.equal(c.role, "follower");
});

test("pre-vote: partitioned follower cannot inflate its term", () => {
  const cluster = makeCluster();
  cluster.tick(10); // C becomes leader, term 1
  const b = cluster.node("B");
  cluster.isolate("B");
  cluster.tick(200); // B times out repeatedly but wins no pre-vote
  assert.equal(b.currentTerm, 1, "term never increases without majority pre-vote");
  assert.notEqual(b.role, "leader");
  // The majority side is undisturbed.
  assert.equal(cluster.node("C").role, "leader");
  assert.equal(cluster.node("C").currentTerm, 1);
});

test("pre-vote: rejected while a leader lease is active", () => {
  const { node, outbox } = makeNode([{ term: 1, command: "e1" }]);
  // Simulate a recently heard-from leader (term 1).
  node.receive({
    kind: "AppendEntriesRequest",
    term: 1, leaderId: "L", prevLogIndex: 1, prevLogTerm: 1, entries: [], leaderCommit: 1,
  });
  outbox.length = 0;
  node.receive({ kind: "PreVoteRequest", term: 2, candidateId: "X", lastLogIndex: 1, lastLogTerm: 1 });
  const resp = outbox.find((m) => m.msg.kind === "PreVoteResponse");
  assert.ok(resp);
  assert.equal((resp.msg as { granted: boolean }).granted, false);
});

test("voting: candidate with a stale log is rejected", () => {
  const { node, outbox } = makeNode([
    { term: 1, command: "e1" },
    { term: 2, command: "e2" },
  ]);
  node.receive({ kind: "RequestVoteRequest", term: 3, candidateId: "X", lastLogIndex: 5, lastLogTerm: 1 });
  const resp = outbox.find((m) => m.msg.kind === "RequestVoteResponse");
  assert.ok(resp);
  assert.equal((resp.msg as { granted: boolean }).granted, false, "older lastLogTerm loses");
  assert.equal(node.votedFor, null);
});

test("consistency check: AppendEntries with mismatched prevLogTerm is rejected", () => {
  const { node, outbox } = makeNode([
    { term: 1, command: "e1" },
    { term: 1, command: "e2" },
  ]);
  node.receive({
    kind: "AppendEntriesRequest",
    term: 2, leaderId: "L", prevLogIndex: 2, prevLogTerm: 9,
    entries: [{ term: 2, command: "x" }], leaderCommit: 0,
  });
  const resp = outbox.find((m) => m.msg.kind === "AppendEntriesResponse");
  assert.ok(resp);
  assert.equal((resp.msg as { success: boolean }).success, false);
  assert.deepEqual(commands(node.log), ["e1", "e2"], "log unchanged");
});

test("safe truncation: committed entries are never overwritten", () => {
  const { node, outbox } = makeNode(
    [
      { term: 1, command: "e1" },
      { term: 1, command: "e2" },
      { term: 1, command: "e3" },
    ],
    2, // e1, e2 committed
  );
  node.receive({
    kind: "AppendEntriesRequest",
    term: 2, leaderId: "L", prevLogIndex: 1, prevLogTerm: 1,
    entries: [{ term: 2, command: "x2" }], // conflicts with committed e2
    leaderCommit: 0,
  });
  const resp = outbox.find((m) => m.msg.kind === "AppendEntriesResponse");
  assert.ok(resp);
  assert.equal((resp.msg as { success: boolean }).success, false);
  assert.deepEqual(commands(node.log), ["e1", "e2", "e3"], "committed prefix intact");
  assert.equal(node.commitIndex, 2);
});

test("safe truncation: uncommitted conflicting suffix is overwritten", () => {
  const { node } = makeNode(
    [
      { term: 1, command: "e1" },
      { term: 1, command: "y2" },
      { term: 1, command: "y3" },
    ],
    1, // only e1 committed
  );
  node.receive({
    kind: "AppendEntriesRequest",
    term: 2, leaderId: "L", prevLogIndex: 1, prevLogTerm: 1,
    entries: [{ term: 2, command: "e2" }],
    leaderCommit: 2,
  });
  assert.deepEqual(commands(node.log), ["e1", "e2"]);
  assert.equal(node.commitIndex, 2);
});

test("term is monotonically non-decreasing", () => {
  const { node, outbox } = makeNode();
  node.receive({
    kind: "AppendEntriesRequest",
    term: 5, leaderId: "L", prevLogIndex: 0, prevLogTerm: 0, entries: [], leaderCommit: 0,
  });
  assert.equal(node.currentTerm, 5);
  outbox.length = 0;

  // Stale messages from lower terms are rejected and never move the term back.
  node.receive({ kind: "RequestVoteRequest", term: 3, candidateId: "X", lastLogIndex: 0, lastLogTerm: 0 });
  node.receive({ kind: "PreVoteRequest", term: 4, candidateId: "Y", lastLogIndex: 0, lastLogTerm: 0 });
  node.receive({
    kind: "AppendEntriesRequest",
    term: 2, leaderId: "Z", prevLogIndex: 0, prevLogTerm: 0, entries: [], leaderCommit: 0,
  });
  assert.equal(node.currentTerm, 5);
  for (const { msg } of outbox) {
    if (msg.kind === "RequestVoteResponse") assert.equal(msg.granted, false);
    if (msg.kind === "PreVoteResponse") assert.equal(msg.granted, false);
    if (msg.kind === "AppendEntriesResponse") assert.equal(msg.success, false);
  }
});
