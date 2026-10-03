import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SafetyOracle } from '../src/invariants.ts';
import { makeCluster, runChecked, runUntilChecked } from './helpers.ts';

test('a five-node cluster elects the lowest-timeout node as single leader', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader() !== undefined, 100));
  assert.equal(cluster.leader()?.id, 'n1');
  assert.equal(cluster.leaders().length, 1);
  assert.deepEqual(cluster.node('n1').leaderBallot, { round: 1, node: 'n1' });
});

test('a minority partition can never elect a leader', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);
  runChecked(cluster, oracle, 80);
  assert.equal(cluster.node('n1').isLeader, false, 'minority side must not lead');
  assert.equal(cluster.node('n2').isLeader, false);
  assert.equal(cluster.node('n3').isLeader, true, 'majority side elects n3');
  assert.deepEqual(cluster.node('n3').leaderBallot, { round: 1, node: 'n3' });
});

test('candidacy requires a majority of promises (2 of 5 is not enough)', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);
  runChecked(cluster, oracle, 200);
  // n1 campaigns forever inside the minority but can never collect 3 promises.
  assert.equal(cluster.node('n1').role, 'candidate');
  assert.ok(cluster.node('n1').sentStats.Prepare > 1, 'candidate retries with fresh rounds');
});

test('a higher-ballot leader deposes the stale leader after healing', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));
  cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.node('n3').isLeader, 300));
  cluster.healAll();
  // n1 may briefly win a re-election with a higher round; the system must
  // still converge to exactly one leader whose ballot dominates.
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leaders().length === 1, 300));
  runChecked(cluster, oracle, 60);
  assert.equal(cluster.leaders().length, 1);
  const leader = cluster.leader()!;
  for (const n of cluster.nodes) {
    if (n === leader) continue;
    assert.equal(n.role, 'follower');
  }
});
