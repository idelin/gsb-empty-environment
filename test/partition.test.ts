import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SafetyOracle } from '../src/invariants.ts';
import { assertInFlightBounded, makeCluster, runChecked, runUntilChecked } from './helpers.ts';

/**
 * Acceptance scenario:
 *  5-node cluster; old leader n1 is cut off with n2; a new leader arises in
 *  the majority {n3,n4,n5}, completes uncommitted slots via Phase 1, safely
 *  overwrites the minority's dirty log, and the cluster reconverges.
 */
test('network partition: old leader replaced, dirty minority log safely overwritten', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();

  // Phase 0: n1 wins the election and commits two client values on all nodes.
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));
  cluster.write('n1', 'cmd-1');
  runChecked(cluster, oracle, 5);
  cluster.write('n1', 'cmd-2');
  runChecked(cluster, oracle, 5);
  for (const n of cluster.nodes) assert.deepEqual(n.applied, ['cmd-1', 'cmd-2']);

  // Phase 1: partition {n1,n2} | {n3,n4,n5}; the old leader's lease lingers.
  cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);

  // Phase 2: dirty write lands only on the minority and can never commit.
  assert.ok(cluster.write('n1', 'dirty').ok);
  runChecked(cluster, oracle, 4);
  assert.equal(cluster.node('n1').commitIndex, 1, 'no quorum: dirty slot must not commit');
  assert.equal(cluster.node('n2').entryAt(2)?.value, 'dirty');

  // Phase 3: the majority side elects n3 via Prepare/Promise (Phase 1).
  // The promise quorum never saw slot 2, so the new leader is free to reuse
  // it; the minority's dirty entry is doomed.
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.node('n3').isLeader, 300));
  runChecked(cluster, oracle, 40);
  assert.equal(cluster.node('n1').isLeader, false, 'old leader lease expired');
  assert.deepEqual(cluster.node('n3').applied, ['cmd-1', 'cmd-2']);

  // Phase 4: the new leader commits fresh client values under its lease,
  // bypassing Phase 1; slot 2 is reused for the new value.
  const preparesBefore = cluster.node('n3').sentStats.Prepare;
  assert.ok(cluster.write('n3', 'cmd-3').ok);
  runChecked(cluster, oracle, 10);
  assert.equal(cluster.node('n3').sentStats.Prepare, preparesBefore, 'no Phase 1 under lease');
  assert.deepEqual(cluster.node('n5').applied, ['cmd-1', 'cmd-2', 'cmd-3']);
  // Minority side is frozen in time: dirty value still uncommitted.
  assert.deepEqual(cluster.node('n1').applied, ['cmd-1', 'cmd-2']);
  assert.deepEqual(cluster.node('n2').applied, ['cmd-1', 'cmd-2']);

  // Phase 5: heal. The old leader is deposed; its dirty slot is overwritten
  // by the chosen value and the whole cluster reconverges.
  cluster.healAll();
  runChecked(cluster, oracle, 200);

  const expected = ['cmd-1', 'cmd-2', 'cmd-3'];
  for (const n of cluster.nodes) {
    assert.deepEqual(n.applied, expected, `${n.id} must converge to the committed log`);
  }
  assert.equal(cluster.node('n1').entryAt(2)?.value, 'cmd-3', 'dirty slot overwritten on n1');
  assert.equal(cluster.node('n2').entryAt(2)?.value, 'cmd-3', 'dirty slot overwritten on n2');
  assert.equal(cluster.leaders().length, 1, 'exactly one stable leader');
  assert.notDeepEqual(cluster.leader()?.leaderBallot, { round: 1, node: 'n1' }, 'old ballot is dead');
  assert.deepEqual(oracle.committedLog(), expected, 'oracle committed log matches');
  assertInFlightBounded(cluster);
});

test('partitioned old leader cannot commit new entries while isolated', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));
  cluster.partition(['n1'], ['n2', 'n3', 'n4', 'n5']);
  assert.ok(cluster.write('n1', 'orphan').ok, 'write accepted locally while lease lingers');
  runChecked(cluster, oracle, 60);
  assert.equal(cluster.node('n1').commitIndex, -1, 'isolated leader commits nothing');
  assert.equal(cluster.node('n1').isLeader, false, 'lease expires without quorum acks');
  assert.ok(cluster.node('n2').isLeader, 'majority side moved on');
});
