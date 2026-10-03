import { test } from 'node:test';
import assert from 'node:assert/strict';
import { NOOP } from '../src/types.ts';
import { SafetyOracle } from '../src/invariants.ts';
import { assertInFlightBounded, makeCluster, runChecked, runUntilChecked } from './helpers.ts';

test('new leader fills acceptor-log holes left by a partitioned leader', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));

  cluster.write('n1', 'v0');
  runChecked(cluster, oracle, 5);

  // Slot 1 reaches only n1,n2,n3: n4 and n5 end up with a hole.
  cluster.network.setDropHook(
    (m) => m.type === 'Accept' && m.slot === 1 && (m.to === 'n4' || m.to === 'n5'),
  );
  cluster.write('n1', 'v1');
  runChecked(cluster, oracle, 5);
  // Keep the hook: the leader's heartbeat repair re-sends must also be dropped.

  cluster.write('n1', 'v2');
  runChecked(cluster, oracle, 5);

  assert.equal(cluster.node('n4').entryAt(1), undefined, 'n4 has a hole at slot 1');
  assert.equal(cluster.node('n4').commitIndex, 0, 'commit must not skip the hole');
  assert.deepEqual(cluster.node('n4').applied, ['v0']);
  assert.equal(cluster.node('n1').commitIndex, 2, 'leader committed the full prefix');

  // Old leader is partitioned away; n3 wins Phase 1 on the majority side.
  cluster.network.setDropHook(null);
  cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.node('n3').isLeader, 300));
  runChecked(cluster, oracle, 40);

  for (const id of ['n3', 'n4', 'n5']) {
    assert.deepEqual(cluster.node(id).applied, ['v0', 'v1', 'v2'], `${id} repaired its hole`);
    assert.equal(cluster.node(id).commitIndex, 2);
  }
  assert.equal(cluster.node('n4').entryAt(1)?.value, 'v1', 'hole filled with the chosen value');
});

test('known holes inside the promise quorum are sealed with NOOP; minority dirty entries are overwritten', () => {
  const cluster = makeCluster();
  const oracle = new SafetyOracle();
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.leader()?.id === 'n1', 100));

  cluster.write('n1', 'v0');
  runChecked(cluster, oracle, 5);

  // Slot 1 reaches nobody but the leader itself: a purely dirty self-write.
  cluster.network.setDropHook((m) => m.type === 'Accept' && m.slot === 1);
  assert.ok(cluster.write('n1', 'dirty').ok);
  runChecked(cluster, oracle, 4);
  // Keep the hook: the leader's heartbeat retransmission must also be dropped.
  assert.equal(cluster.node('n1').commitIndex, 0, 'a lone acceptor can never commit');

  // Slot 2 is accepted by everyone, but commit stays blocked behind slot 1.
  cluster.write('n1', 'v2');
  runChecked(cluster, oracle, 5);
  assert.equal(cluster.node('n1').commitIndex, 0, 'commit cannot skip the uncommitted slot 1');
  assert.equal(cluster.node('n4').entryAt(2)?.value, 'v2');
  assert.equal(cluster.node('n4').entryAt(1), undefined, 'n4 never saw slot 1');

  // n1 is partitioned away. The new leader's promise quorum reports slots 0
  // and 2 but nothing for slot 1, so slot 1 is sealed with NOOP.
  cluster.network.setDropHook(null);
  cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);
  assert.ok(runUntilChecked(cluster, oracle, () => cluster.node('n3').isLeader, 300));
  runChecked(cluster, oracle, 40);
  for (const id of ['n3', 'n4', 'n5']) {
    assert.deepEqual(cluster.node(id).applied, ['v0', NOOP, 'v2'], `${id} sealed the hole`);
  }

  cluster.healAll();
  runChecked(cluster, oracle, 200);
  for (const n of cluster.nodes) {
    assert.deepEqual(n.applied, ['v0', NOOP, 'v2'], `${n.id} converged; dirty value is gone`);
  }
  assert.equal(cluster.node('n1').entryAt(1)?.value, NOOP, 'minority dirty entry overwritten');
  assertInFlightBounded(cluster);
});
