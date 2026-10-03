import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SafetyOracle } from '../src/invariants.ts';
import { makeCluster, runChecked, runUntilChecked } from './helpers.ts';

test('leader lease bypasses Phase 1 for consecutive client writes', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));
  const leader = cluster.node('n1');
  const electionPrepares = leader.sentStats.Prepare;
  assert.equal(electionPrepares, 4, 'exactly one Prepare round: one message per peer');

  for (const v of ['a', 'b', 'c']) {
    const r = cluster.write('n1', v);
    assert.ok(r.ok);
    runChecked(cluster, oracle, 5);
  }
  assert.equal(leader.sentStats.Prepare, electionPrepares, 'no Phase 1 traffic while the lease holds');
  assert.ok(leader.leaseActive);
  runChecked(cluster, oracle, 5);
  for (const n of cluster.nodes) {
    assert.deepEqual(n.applied, ['a', 'b', 'c'], `${n.id} must commit the contiguous prefix`);
    assert.equal(n.commitIndex, 2);
  }
});

test('no commit without a majority; commit completes after healing', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));
  cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);
  const r = cluster.write('n1', 'x');
  assert.ok(r.ok);
  runChecked(cluster, oracle, 4);
  assert.equal(cluster.node('n1').commitIndex, -1, 'minority acks must not commit');
  assert.equal(cluster.node('n2').commitIndex, -1);
  assert.equal(cluster.node('n2').entryAt(0)?.value, 'x', 'minority holds the dirty entry');

  cluster.healAll();
  // The dropped Accepts are not retransmitted; a fresh election repairs the
  // slot (the promise quorum sees the dirty entry) and commits it everywhere.
  runChecked(cluster, oracle, 80);
  for (const n of cluster.nodes) {
    assert.deepEqual(n.applied, ['x'], 'post-heal repair commits the entry everywhere');
  }
});

test('lease expires without quorum acks; majority side replaces the leader', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));
  cluster.partition(['n1'], ['n2', 'n3', 'n4', 'n5']);
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.node('n2').isLeader, 300));
  runChecked(cluster, oracle, 15); // let the isolated leader's lease run out
  assert.equal(cluster.node('n1').isLeader, false, 'isolated old leader must step down');
  cluster.healAll();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leaders().length === 1, 300));
});

test('heartbeat renews the lease: an idle leader survives many lease periods', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));
  runChecked(cluster, oracle, 120); // leaseDuration is 30 ticks
  assert.equal(cluster.node('n1').isLeader, true);
  assert.ok(cluster.node('n1').leaseActive);
  assert.equal(cluster.leaders().length, 1);
});

test('client writes to a non-leader are rejected', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader() !== undefined, 100));
  const r = cluster.write('n3', 'nope');
  assert.equal(r.ok, false);
  assert.equal(r.reason, 'not-leader');
});
