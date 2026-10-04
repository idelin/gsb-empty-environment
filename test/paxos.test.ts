import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import { Cluster } from '../src/cluster.ts';
import { EntryType } from '../src/codec.ts';

const OPTS = { leaseMs: 60_000, rpcTimeoutMs: 150 };

function committedPayloads(cluster: Cluster, id: number): string[] {
  const node = cluster.node(id);
  const out: string[] = [];
  for (let i = 1; i <= node.commitIndex; i++) {
    const v = node.entryAt(i);
    assert.ok(v, `node ${id} hole at slot ${i}`);
    out.push(v.type === EntryType.Noop ? '<noop>' : v.payload.toString());
  }
  return out;
}

function assertLogConverged(cluster: Cluster, expected: string[]): void {
  for (const n of cluster.nodes) {
    assert.deepEqual(committedPayloads(cluster, n.id), expected, `node ${n.id} diverged`);
  }
}

test('election + basic replication commits entries in order without holes', async () => {
  const cluster = Cluster.create(5, OPTS);
  try {
    const leader = cluster.node(1);
    assert.equal(await leader.campaign(), true);
    assert.equal(leader.currentRole, 'leader');
    await leader.propose(Buffer.from('A'));
    await leader.propose(Buffer.from('B'));
    await leader.propose(Buffer.from('C'));
    await cluster.settle();
    assertLogConverged(cluster, ['A', 'B', 'C']);
  } finally {
    await cluster.stop();
  }
});

test('leader lease skips Phase 1 for consecutive commits', async () => {
  const cluster = Cluster.create(5, OPTS);
  try {
    const leader = cluster.node(1);
    await leader.campaign();
    await cluster.settle();
    const preparesAfterElection = cluster.transport.sentCount('prepare');
    await leader.propose(Buffer.from('x'));
    await leader.propose(Buffer.from('y'));
    await leader.propose(Buffer.from('z'));
    await cluster.settle();
    assert.equal(cluster.transport.sentCount('prepare'), preparesAfterElection);
    assertLogConverged(cluster, ['x', 'y', 'z']);
  } finally {
    await cluster.stop();
  }
});

test('expired lease forces a fresh Phase 1 before the next commit', async () => {
  const cluster = Cluster.create(5, { leaseMs: 30, rpcTimeoutMs: 150 });
  try {
    const leader = cluster.node(1);
    await leader.campaign();
    await leader.propose(Buffer.from('first'));
    const prepares = cluster.transport.sentCount('prepare');
    await new Promise((r) => setTimeout(r, 60));
    await leader.propose(Buffer.from('second'));
    assert.ok(cluster.transport.sentCount('prepare') > prepares);
    await cluster.settle();
    assertLogConverged(cluster, ['first', 'second']);
  } finally {
    await cluster.stop();
  }
});

test('non-leader cannot propose', async () => {
  const cluster = Cluster.create(5, OPTS);
  try {
    await cluster.node(1).campaign();
    await assert.rejects(() => cluster.node(3).propose(Buffer.from('nope')), /not leader/);
  } finally {
    await cluster.stop();
  }
});

test('ACCEPTANCE: partition replaces old leader, repairs slots, overwrites dirty minority log', async () => {
  const cluster = Cluster.create(5, OPTS);
  try {
    const oldLeader = cluster.node(1);
    await oldLeader.campaign();
    await oldLeader.propose(Buffer.from('A'));
    await oldLeader.propose(Buffer.from('B'));
    await cluster.settle();
    assertLogConverged(cluster, ['A', 'B']);

    // Network partition: old leader + one follower vs the majority.
    cluster.partition([1, 2]);

    // Old leader keeps accepting writes but cannot reach a quorum:
    // slots 3 and 4 become uncommitted dirty entries on the minority side.
    await assert.rejects(oldLeader.propose(Buffer.from('C-dirty')));
    await assert.rejects(oldLeader.propose(Buffer.from('D-dirty')));
    assert.equal(oldLeader.entryAt(3)?.payload.toString(), 'C-dirty');
    assert.equal(cluster.node(2).entryAt(4)?.payload.toString(), 'D-dirty');
    assert.equal(cluster.node(3).entryAt(3), undefined);

    // Majority side elects a new leader; Phase 1 finds no chosen values
    // beyond slot 2, so it starts fresh at slot 3.
    const newLeader = cluster.node(4);
    assert.equal(await newLeader.campaign(), true);
    assert.ok(newLeader.currentBallot.term > oldLeader.currentBallot.term ||
      (newLeader.currentBallot.term === oldLeader.currentBallot.term && newLeader.id > oldLeader.id));
    await newLeader.propose(Buffer.from('X'));
    await newLeader.propose(Buffer.from('Y'));
    await cluster.settle();
    for (const id of [3, 4, 5]) {
      assert.deepEqual(committedPayloads(cluster, id), ['A', 'B', 'X', 'Y']);
    }
    // Old leader is still partitioned and must not have committed anything new.
    assert.equal(oldLeader.commitIndex, 2);

    // Heal: the next proposal forces nodes 1 and 2 to truncate their dirty
    // suffix (C-dirty/D-dirty) and adopt the majority log via backfill.
    cluster.heal();
    await newLeader.propose(Buffer.from('Z'));
    await cluster.settle();

    assertLogConverged(cluster, ['A', 'B', 'X', 'Y', 'Z']);
    // Dirty entries are gone everywhere, including the minority side.
    for (const n of cluster.nodes) {
      assert.notEqual(n.entryAt(3)?.payload.toString(), 'C-dirty');
      assert.notEqual(n.entryAt(4)?.payload.toString(), 'D-dirty');
      assert.equal(n.lastIndex, 5);
    }
    // Old leader stepped down and recognizes the higher ballot.
    assert.equal(oldLeader.currentRole, 'follower');
    // Previously committed values survived the whole failover.
    assert.deepEqual(committedPayloads(cluster, 1).slice(0, 2), ['A', 'B']);
  } finally {
    await cluster.stop();
  }
});

test('safety: value accepted by any majority acceptor is re-proposed, not no-op filled', async () => {
  const cluster = Cluster.create(5, OPTS);
  try {
    const leader1 = cluster.node(1);
    await leader1.campaign();
    await leader1.propose(Buffer.from('committed'));

    // Let slot 2 reach node 3 (future majority side) but not 4 and 5.
    cluster.transport.block(1, 4);
    cluster.transport.block(1, 5);
    const p = leader1.propose(Buffer.from('maybe-chosen'));
    await cluster.settle();
    // Quorum {1,2,3} exists, so the proposal actually commits on that side.
    await p;
    assert.equal(cluster.node(3).entryAt(2)?.payload.toString(), 'maybe-chosen');

    // Partition {1,2} away; node 3 carries the value into the new majority.
    cluster.partition([1, 2]);
    const leader2 = cluster.node(4);
    await leader2.campaign();
    await cluster.settle();
    // Phase 1 must have adopted the value seen at node 3 into slot 2.
    assert.equal(cluster.node(4).entryAt(2)?.payload.toString(), 'maybe-chosen');
    // The next proposal forces lagging node 5 to backfill the adopted prefix.
    await leader2.propose(Buffer.from('after'));
    await cluster.settle();
    assert.equal(cluster.node(5).entryAt(2)?.payload.toString(), 'maybe-chosen');
    assert.deepEqual(committedPayloads(cluster, 4), ['committed', 'maybe-chosen', 'after']);
    assert.deepEqual(committedPayloads(cluster, 5), ['committed', 'maybe-chosen', 'after']);
  } finally {
    await cluster.stop();
  }
});

test('hole repair: Phase 1 fills missing slots with no-op to keep the log contiguous', async () => {
  const cluster = Cluster.create(5, OPTS);
  try {
    const leader1 = cluster.node(1);
    await leader1.campaign();
    await leader1.propose(Buffer.from('s1'));

    // Simulate a replica that crashed mid-replication: node 3 holds an
    // uncommitted slot 3 while slot 2 was never written anywhere.
    const { encodeBallot } = await import('../src/paxos.ts');
    await cluster.node(3).injectUncommitted(encodeBallot({ term: 1, node: 1 }), 3, EntryType.Data, Buffer.from('s3-orphan'));
    assert.equal(cluster.node(3).entryAt(3)?.payload.toString(), 's3-orphan');
    assert.equal(cluster.node(3).entryAt(2), undefined);

    // Isolate the old leader alone; the remaining four elect a new leader.
    cluster.partition([1]);
    const leader2 = cluster.node(4);
    await leader2.campaign();
    await cluster.settle();

    // Slot 2 was chosen by nobody: filled with a committed no-op.
    // Slot 3 was accepted by node 3: re-proposed and committed, not erased.
    for (const id of [2, 3, 4, 5]) {
      assert.deepEqual(committedPayloads(cluster, id), ['s1', '<noop>', 's3-orphan']);
    }
    assert.equal(cluster.node(4).entryAt(2)?.type, EntryType.Noop);
  } finally {
    await cluster.stop();
  }
});

test('minority dirty suffix is truncated and rewritten after heal', async () => {
  const cluster = Cluster.create(5, OPTS);
  try {
    const leader1 = cluster.node(1);
    await leader1.campaign();
    await leader1.propose(Buffer.from('s1'));

    cluster.partition([1, 2]);
    await assert.rejects(leader1.propose(Buffer.from('s2-minority')));
    await assert.rejects(leader1.propose(Buffer.from('s3-minority')));
    assert.equal(cluster.node(2).entryAt(3)?.payload.toString(), 's3-minority');

    const leader2 = cluster.node(3);
    await leader2.campaign();
    await leader2.propose(Buffer.from('s2-new'));
    cluster.heal();
    await leader2.propose(Buffer.from('s3-new'));
    await cluster.settle();
    assertLogConverged(cluster, ['s1', 's2-new', 's3-new']);
    for (const n of cluster.nodes) {
      assert.notEqual(n.entryAt(2)?.payload.toString(), 's2-minority');
      assert.notEqual(n.entryAt(3)?.payload.toString(), 's3-minority');
    }
  } finally {
    await cluster.stop();
  }
});

test('no timers or listeners leak after cluster stop', async () => {
  const activeTimeouts = () =>
    process.getActiveResourcesInfo().filter((r) => r === 'Timeout' || r === 'Immediate').length;
  const timersBefore = activeTimeouts();
  const cluster = Cluster.create(5, { leaseMs: 60_000, rpcTimeoutMs: 150 });
  const leader = cluster.node(1);
  await leader.campaign();
  await leader.propose(Buffer.from('leak-check'));
  // Partition to force in-flight RPC timeouts (pending timers), then stop.
  cluster.partition([1, 2]);
  const p = leader.propose(Buffer.from('will-time-out')).catch(() => 'rejected');
  cluster.heal();
  await cluster.stop();
  await p;
  for (const n of cluster.nodes) {
    assert.equal(n.activeTimers, 0, `node ${n.id} leaked timers`);
  }
  assert.equal(cluster.transport.pending, 0);
  const timersAfter = activeTimeouts();
  assert.ok(timersAfter <= timersBefore, `timers leaked: ${timersBefore} -> ${timersAfter}`);
});
