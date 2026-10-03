import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import type { Command } from '../src/types.ts';

const client = (data: string): Command => ({ kind: 'client', id: data, data });

test('elects a single leader and commits entries to all nodes', () => {
  const c = new Cluster(5);
  const leader = c.electLeader();
  assert.equal(leader.id, 1);
  assert.equal(c.nodes.filter((n) => n.role === 'leader').length, 1);

  leader.propose(client('A'));
  leader.propose(client('B'));

  for (const node of c.nodes) {
    assert.equal(node.commitIndex, 2, `node ${node.id} commitIndex`);
    assert.deepEqual(
      node.applied.map((e) => e.value),
      [client('A'), client('B')],
    );
  }
});

test('lease fast path: consecutive entries commit without Phase 1', () => {
  const c = new Cluster(5);
  const leader = c.electLeader();
  const preparesAfterElection = c.network.count('prepare');
  assert.ok(preparesAfterElection > 0);

  for (const v of ['A', 'B', 'C', 'D', 'E']) leader.propose(client(v));

  assert.equal(c.network.count('prepare'), preparesAfterElection, 'no Prepare under valid lease');
  assert.equal(c.network.count('accept'), 5 * 4, 'one Accept broadcast per entry');
  assert.equal(leader.commitIndex, 5);
});

test('propose rejects non-leader and expired lease', () => {
  const c = new Cluster(5);
  const leader = c.electLeader();
  assert.throws(() => c.node(2).propose(client('X')), /not leader/);

  // Stop heartbeats from helping: isolate the leader, then let the lease lapse.
  c.partition([1], [2, 3, 4, 5]);
  // Lease is extended by heartbeats while ticking; freeze by not ticking and
  // instead jump the clock directly past the lease.
  c.now += c.leaseMs + 1;
  assert.throws(() => leader.propose(client('Y')), /lease expired/);
});

test('no-gap commit: lagging follower never commits past a hole', () => {
  const c = new Cluster(5);
  const leader = c.electLeader();
  leader.propose(client('A')); // slot 1 everywhere

  // n3 misses slot 2.
  c.network.setLink(1, 3, false);
  leader.propose(client('B')); // slot 2: majority {1,2,4,5}
  c.network.setLink(1, 3, true);

  leader.propose(client('C')); // slot 3 reaches n3, but slot 2 is a hole there
  const n3 = c.node(3);
  // n3 must not apply C before B; catch-up fills the hole deterministically.
  assert.deepEqual(
    n3.applied.map((e) => e.value),
    [client('A'), client('B'), client('C')],
    'contiguous prefix, hole repaired via catch-up',
  );
  assert.equal(n3.commitIndex, 3);
});

test('stale-ballot accept is rejected and cannot commit', () => {
  const c = new Cluster(5);
  const leader = c.electLeader(); // ballot {1,1}
  leader.propose(client('A'));

  // n2 campaigns with a higher ballot; n1's later accepts must be rejected.
  c.partition([1], [2, 3, 4, 5]);
  c.runUntil(() => c.nodes.some((n) => n.id !== 1 && n.role === 'leader'));
  // The isolated old leader still believes it leads; the majority has moved on.
  const newLeader = c.nodes.find((n) => n.id !== 1 && n.role === 'leader');
  assert.ok(newLeader && newLeader.id !== 1);

  const rejectsBefore = c.network.count('reject');
  // Old leader can still propose under its self-held lease, but only reaches
  // a minority that already promised a higher ballot: rejected, never commits.
  c.network.setLink(1, 4, true);
  c.network.setLink(4, 1, true);
  leader.propose(client('stale'));
  assert.equal(leader.commitIndex, 1, 'stale entry must not commit');
  assert.ok(c.network.count('reject') > rejectsBefore, 'minority rejects stale ballot');
  assert.equal(leader.role, 'follower', 'old leader steps down on higher-ballot reject');

  c.heal();
  const target = newLeader.commitIndex;
  c.runUntil(() => c.nodes.every((n) => n.commitIndex === target));
  for (const node of c.nodes) {
    assert.ok(
      node.applied.every((e) => !(e.value.kind === 'client' && e.value.data === 'stale')),
      `node ${node.id} never commits the stale entry`,
    );
  }
});
