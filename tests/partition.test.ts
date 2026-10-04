import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster, Client } from '../src/cluster.ts';
import { NotLeaderError, type AcceptReply, type Command } from '../src/node.ts';
import { assertNoLeaks } from './helpers.ts';

/**
 * Acceptance scenario:
 * 5-node cluster; the old leader is partitioned away with one follower;
 * a new leader is elected by the majority, completes Phase 1, repairs
 * uncommitted slots and safely overwrites the minority's dirty log.
 */
test('network partition: old leader replaced, dirty minority log overwritten', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 60_000 });
  const client = new Client('app', cluster);

  // 1. N1 becomes leader and commits A at slot 1 on all 5 nodes.
  const n1 = cluster.node(1);
  await n1.campaign();
  assert.equal(await client.submit('A', { prefer: n1 }), 1);
  await cluster.settle();
  for (const n of cluster.nodes) assert.equal(n.commitIndex, 1);

  // 2. Partition {1,2} | {3,4,5}. The old leader keeps its lease but loses quorum.
  cluster.partition([1, 2], [3, 4, 5]);

  // 3. A dirty value X is accepted only inside the minority (never committed).
  const dirty: Command = { clientId: 'app', seq: 100, data: 'X-dirty' };
  const b1 = n1.currentBallot;
  for (const target of [1, 2]) {
    const reply = await cluster.net.call<AcceptReply>(1, target, {
      type: 'accept',
      slot: 2,
      ballot: b1,
      value: dirty,
    });
    assert.equal(reply.ok, true);
  }
  assert.equal(cluster.node(2).entryAt(2)?.value?.data, 'X-dirty');

  // 4. The partitioned old leader cannot commit anything new.
  await assert.rejects(n1.propose({ clientId: 'app', seq: 101, data: 'Y' }), NotLeaderError);
  assert.equal(n1.commitIndex, 1, 'minority leader must not advance its watermark');

  // 5. The majority elects N3; Phase 1 runs, then B and C are committed.
  const n3 = cluster.node(3);
  assert.equal(await n3.campaign(), true, 'majority side can elect a leader');
  assert.equal(await client.submit('B', { prefer: n3 }), 2);
  assert.equal(await client.submit('C', { prefer: n3 }), 3);
  await cluster.settle();

  for (const id of [3, 4, 5]) {
    assert.deepEqual(
      cluster.node(id).committedValues().map((v) => v?.data ?? null),
      ['A', 'B', 'C'],
      `majority node ${id} commits through the partition`,
    );
  }
  // Minority is stuck at the old watermark with its dirty entry intact.
  assert.equal(n1.commitIndex, 1);
  assert.equal(cluster.node(2).commitIndex, 1);

  // 6. Heal. The new leader's commit notices overwrite the dirty minority log.
  cluster.heal();
  await n3.broadcastCommits();
  await cluster.settle();

  for (const n of cluster.nodes) {
    assert.deepEqual(
      n.committedValues().map((v) => v?.data ?? null),
      ['A', 'B', 'C'],
      `node ${n.id} converges after healing`,
    );
    assert.ok(
      !n.committedValues().some((v) => v?.data === 'X-dirty'),
      `node ${n.id} must never commit the dirty minority value`,
    );
  }
  // The dirty slot-2 entry on N1/N2 was replaced by the committed value B.
  assert.equal(cluster.node(2).entryAt(2)?.value?.data, 'B');

  // 7. The replaced leader can rejoin: a new write through N1 is either
  //    redirected or N1 legitimately re-wins Phase 1 with a higher ballot —
  //    either way previously committed values are preserved.
  const slot = await client.submit('D', { prefer: n1 });
  assert.equal(slot, 4);
  for (const n of cluster.leaders()) await n.broadcastCommits();
  await cluster.settle();
  for (const n of cluster.nodes) {
    assert.deepEqual(
      n.committedValues().map((v) => v?.data ?? null),
      ['A', 'B', 'C', 'D'],
      `node ${n.id} final committed log`,
    );
  }
  await assertNoLeaks(cluster);
});

test('new leader repairs holes with no-op fillers before committing', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 60_000 });
  const n1 = cluster.node(1);

  // Commit v1 at slot 1 everywhere.
  await n1.campaign();
  const b1 = n1.currentBallot;
  await n1.propose({ clientId: 'c', seq: 0, data: 'v1' });
  await cluster.settle();
  assert.equal(n1.commitIndex, 1);

  // Inject v3 at slot 3 on {1,2,3} only, leaving slot 2 an uncommitted hole.
  const v3: Command = { clientId: 'c', seq: 2, data: 'v3' };
  for (const target of [1, 2, 3]) {
    const reply = await cluster.net.call<AcceptReply>(1, target, {
      type: 'accept',
      slot: 3,
      ballot: b1,
      value: v3,
    });
    assert.equal(reply.ok, true);
  }

  // Isolate the old leader; N2 wins Phase 1 in the majority and must repair.
  cluster.partition([1, 5], [2, 3, 4]);
  const n2 = cluster.node(2);
  assert.equal(await n2.campaign(), true);
  await cluster.settle();

  for (const id of [2, 3, 4]) {
    const n = cluster.node(id);
    assert.equal(n.commitIndex, 3, `node ${id} watermark after repair`);
    const values = n.committedValues();
    assert.equal(values[0]?.data, 'v1');
    assert.equal(values[1], null, 'slot 2 hole is filled with a no-op');
    assert.equal(values[2]?.data, 'v3', 'highest-ballot value survives repair');
  }

  // Heal: the whole cluster converges to the repaired log.
  cluster.heal();
  await n2.broadcastCommits();
  await cluster.settle();
  for (const n of cluster.nodes) {
    assert.deepEqual(
      n.committedValues().map((v) => v?.data ?? null),
      ['v1', null, 'v3'],
      `node ${n.id} converges to the repaired log`,
    );
  }
  await assertNoLeaks(cluster);
});

test('minority partition can neither elect nor commit', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 60_000 });
  cluster.partition([1, 2], [3, 4, 5]);

  assert.equal(await cluster.node(1).campaign(), false, 'no quorum in minority');
  assert.equal(cluster.node(1).isLeader, false);

  // The majority side still makes progress.
  const n4 = cluster.node(4);
  assert.equal(await n4.campaign(), true);
  const client = new Client('app', cluster);
  assert.equal(await client.submit('only-majority', { prefer: n4 }), 1);
  await cluster.settle();

  assert.equal(cluster.node(1).commitIndex, 0);
  assert.equal(cluster.node(2).commitIndex, 0);
  assert.equal(cluster.node(4).commitIndex, 1);
  await assertNoLeaks(cluster);
});

test('flapping partition never violates agreement', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 60_000 });
  const client = new Client('app', cluster);

  // Phase A: N1 commits 'a1' on everyone.
  await cluster.node(1).campaign();
  await client.submit('a1', { prefer: cluster.node(1) });
  await cluster.settle();

  // Phase B: partition, majority commits 'b2' under N5.
  cluster.partition([1, 2], [3, 4, 5]);
  await cluster.node(5).campaign();
  await client.submit('b2', { prefer: cluster.node(5) });
  await cluster.settle();

  // Phase C: re-partition the other way; N1 must not resurrect stale state.
  cluster.heal();
  cluster.partition([4, 5], [1, 2, 3]);
  await cluster.node(2).campaign();
  await client.submit('c3', { prefer: cluster.node(2) });
  cluster.heal();
  for (const n of cluster.leaders()) await n.broadcastCommits();
  await cluster.settle();

  for (const n of cluster.nodes) {
    assert.deepEqual(
      n.committedValues().map((v) => v?.data ?? null),
      ['a1', 'b2', 'c3'],
      `node ${n.id} final log after flapping partitions`,
    );
  }
  await assertNoLeaks(cluster);
});
