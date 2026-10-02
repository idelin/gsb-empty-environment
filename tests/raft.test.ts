import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { Clock } from '../src/clock.ts';
import { Network } from '../src/network.ts';
import { RaftNode } from '../src/raft.ts';

/**
 * Deterministic 3-node cluster.
 * C has the shortest election timeout and becomes the term-1 leader;
 * A wins whenever C is partitioned away; B is the slowest.
 */
function makeCluster(): Cluster {
  return new Cluster(
    [
      { id: 'A', electionTimeout: 25 },
      { id: 'B', electionTimeout: 40 },
      { id: 'C', electionTimeout: 10 },
    ],
    4,
  );
}

test('acceptance: term 1 commits 3, partitioned majority reaches term 2 with 4 more, healed old leader rolls back uncommitted entries', () => {
  const cluster = makeCluster();
  const [a, b, c] = [cluster.node('A'), cluster.node('B'), cluster.node('C')];

  // ---- Phase 1: term 1, leader C commits 3 entries on all nodes ----
  cluster.run(30);
  assert.equal(c.role, 'leader');
  assert.equal(c.currentTerm, 1);

  c.append('x1');
  c.append('x2');
  c.append('x3');
  cluster.run(12);
  for (const node of [a, b, c]) {
    assert.equal(node.commitIndex, 3, `${node.id} commitIndex`);
    assert.deepEqual(node.applied, ['x1', 'x2', 'x3']);
  }

  // ---- Phase 2: isolate C; C keeps 2 uncommitted entries locally ----
  cluster.isolate('C');
  c.append('u1'); // replicated nowhere: C is cut off
  c.append('u2');
  cluster.run(80);

  // Majority partition A/B still progresses: new election, term 2.
  assert.equal(a.role, 'leader');
  assert.equal(a.currentTerm, 2);
  assert.equal(b.currentTerm, 2);

  // Pre-vote saved C: isolated, it never gathered a pre-vote majority,
  // so its term stayed at 1 despite many election timeouts.
  assert.ok(c.preVoteAttempts > 0);
  assert.equal(c.currentTerm, 1);
  assert.equal(c.log.length, 5); // 3 committed + 2 uncommitted
  assert.equal(c.commitIndex, 3);

  // A/B commit 4 more entries (indexes 4..7) in term 2.
  a.append('y1');
  a.append('y2');
  a.append('y3');
  a.append('y4');
  cluster.run(15);
  assert.equal(a.commitIndex, 7);
  assert.equal(b.commitIndex, 7);
  assert.deepEqual(b.applied, ['x1', 'x2', 'x3', 'y1', 'y2', 'y3', 'y4']);
  assert.equal(c.commitIndex, 3); // C untouched while partitioned

  // ---- Phase 3: heal C; higher-term heartbeats force rollback ----
  const committedPrefixBefore = c.log.slice(0, 3).map((e) => e.command);
  cluster.heal('C');
  cluster.run(40);

  // C stepped down, adopted term 2, truncated u1/u2 and took A's log.
  assert.equal(c.role, 'follower');
  assert.equal(c.currentTerm, 2);
  assert.deepEqual(c.log, a.log);
  assert.equal(c.log.length, 7);
  assert.equal(c.commitIndex, 7);
  assert.deepEqual(c.applied, ['x1', 'x2', 'x3', 'y1', 'y2', 'y3', 'y4']);

  // Committed entries were never lost or rewritten.
  assert.deepEqual(committedPrefixBefore, ['x1', 'x2', 'x3']);
  assert.deepEqual(c.log.slice(0, 3).map((e) => e.command), ['x1', 'x2', 'x3']);

  // Terms were monotonically non-decreasing on every node.
  for (const node of [a, b, c]) {
    const history = node.termHistory;
    for (let i = 1; i < history.length; i++) {
      assert.ok(history[i] > history[i - 1], `${node.id} term history ${history}`);
    }
  }
  assert.deepEqual(c.termHistory, [0, 1, 2]);
});

test('pre-vote: isolated node cannot raise its term', () => {
  const cluster = makeCluster();
  const c = cluster.node('C');
  cluster.run(30);
  assert.equal(c.role, 'leader');

  cluster.isolate('C');
  cluster.run(100);
  assert.ok(c.preVoteAttempts >= 5);
  assert.equal(c.currentTerm, 1, 'term must not grow while partitioned');
});

test('log consistency check: follower rejects mismatched prevLogTerm', () => {
  const clock = new Clock();
  const network = new Network(clock);
  network.latency = 0;
  const node = new RaftNode({
    id: 'N',
    members: ['N', 'L', 'M'],
    electionTimeout: 1000,
    heartbeatInterval: 4,
    clock,
    network,
  });
  let reply: unknown = null;
  network.register('L', (msg) => {
    reply = msg;
  });
  const flush = () => network.deliverDue();

  node.receive({
    type: 'AppendEntries',
    term: 1,
    from: 'L',
    prevLogIndex: 0,
    prevLogTerm: 0,
    entries: [{ term: 1, command: 'a' }],
    leaderCommit: 0,
  });
  flush();
  assert.deepEqual(reply, { type: 'AppendEntriesReply', term: 1, from: 'N', success: true, matchIndex: 1 });

  // Same index, wrong term -> consistency check fails, entry not appended.
  node.receive({
    type: 'AppendEntries',
    term: 1,
    from: 'L',
    prevLogIndex: 1,
    prevLogTerm: 9,
    entries: [{ term: 1, command: 'b' }],
    leaderCommit: 0,
  });
  flush();
  assert.deepEqual(reply, { type: 'AppendEntriesReply', term: 1, from: 'N', success: false, matchIndex: 1 });
  assert.equal(node.log.length, 1);
});

test('safe truncation: committed entries can never be truncated', () => {
  const clock = new Clock();
  const network = new Network(clock);
  const node = new RaftNode({
    id: 'N',
    members: ['N', 'L', 'M'],
    electionTimeout: 1000,
    heartbeatInterval: 4,
    clock,
    network,
  });
  node.receive({
    type: 'AppendEntries',
    term: 1,
    from: 'L',
    prevLogIndex: 0,
    prevLogTerm: 0,
    entries: [
      { term: 1, command: 'a' },
      { term: 1, command: 'b' },
    ],
    leaderCommit: 2,
  });
  assert.equal(node.commitIndex, 2);

  // A conflicting append at a committed index must be refused loudly.
  assert.throws(
    () =>
      node.receive({
        type: 'AppendEntries',
        term: 1,
        from: 'L',
        prevLogIndex: 0,
        prevLogTerm: 0,
        entries: [{ term: 2, command: 'evil' }],
        leaderCommit: 2,
      }),
    /safety violation/,
  );
  assert.deepEqual(node.applied, ['a', 'b']);
});

test('election: one vote per term and higher term forces step-down', () => {
  const clock = new Clock();
  const network = new Network(clock);
  network.latency = 0;
  const node = new RaftNode({
    id: 'N',
    members: ['N', 'P', 'Q'],
    electionTimeout: 1000,
    heartbeatInterval: 4,
    clock,
    network,
  });
  const replies: unknown[] = [];
  network.register('P', (msg) => replies.push(msg));
  network.register('Q', (msg) => replies.push(msg));
  const flush = () => network.deliverDue();

  const voteReq = (from: string, term: number) =>
    ({ type: 'RequestVote', term, from, lastLogIndex: 0, lastLogTerm: 0 }) as const;

  node.receive(voteReq('P', 1));
  flush();
  node.receive(voteReq('Q', 1)); // same term: already voted for P
  flush();
  assert.deepEqual(replies[0], { type: 'RequestVoteReply', term: 1, from: 'N', granted: true });
  assert.deepEqual(replies[1], { type: 'RequestVoteReply', term: 1, from: 'N', granted: false });

  node.receive(voteReq('Q', 2)); // higher term resets the vote
  flush();
  assert.deepEqual(replies[2], { type: 'RequestVoteReply', term: 2, from: 'N', granted: true });
  assert.equal(node.currentTerm, 2);

  node.receive(voteReq('P', 1)); // stale term is rejected outright
  flush();
  assert.deepEqual(replies[3], { type: 'RequestVoteReply', term: 2, from: 'N', granted: false });
});
