import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { InvariantMonitor } from '../src/monitor.ts';
import { NOOP_VALUE } from '../src/types.ts';

function makeCluster(size = 5): { c: Cluster; mon: InvariantMonitor } {
  const c = new Cluster(size);
  const mon = new InvariantMonitor(c);
  return { c, mon };
}

test('elects a single leader via Prepare/Promise majority', () => {
  const { c, mon } = makeCluster();
  c.elect('A');
  c.runUntilIdle();
  assert.equal(c.leader()?.id, 'A');
  assert.equal(c.node('A').role, 'leader');
  assert.ok((c.net.sentByKind.get('prepare') ?? 0) >= 4);
  assert.ok((c.net.sentByKind.get('promise') ?? 0) >= c.majority - 1);
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('minority candidate cannot become leader', () => {
  const { c, mon } = makeCluster();
  c.partition([['A', 'B'], ['C', 'D', 'E']]);
  c.elect('A');
  c.runUntilIdle();
  assert.notEqual(c.node('A').role, 'leader');
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('leader lease bypasses Phase 1 for consecutive entries', () => {
  const { c, mon } = makeCluster();
  c.elect('A');
  c.runUntilIdle();
  const preparesAfterElection = c.net.sentByKind.get('prepare') ?? 0;
  for (let i = 0; i < 8; i++) c.propose('A', `v${i}`);
  c.runUntilIdle();
  assert.equal(c.net.sentByKind.get('prepare'), preparesAfterElection);
  assert.equal(c.node('A').commitIndex, 7);
  for (const id of c.nodeIds) {
    assert.deepEqual(
      c.node(id).committedPrefix(),
      Array.from({ length: 8 }, (_, i) => `v${i}`),
    );
  }
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('commit prefix is gap-free and contiguous', () => {
  const { c, mon } = makeCluster();
  c.elect('A');
  c.runUntilIdle();
  for (let i = 0; i < 20; i++) c.propose('A', `cmd-${i}`);
  c.runUntilIdle();
  for (const id of c.nodeIds) {
    const n = c.node(id);
    assert.equal(n.commitIndex, 19);
    for (let s = 0; s <= n.commitIndex; s++) {
      assert.ok(n.log.get(s)?.committed, `slot ${s} committed on ${id}`);
    }
  }
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('lease expiry steps the leader down; next proposal needs Phase 1', () => {
  const { c, mon } = makeCluster(5);
  c.elect('A');
  c.runUntilIdle();
  c.propose('A', 'x');
  c.runUntilIdle();
  c.tick(150); // leaseDuration = 100
  assert.equal(c.node('A').role, 'follower');
  const accepted = c.propose('A', 'y');
  assert.equal(accepted, false); // queued, not leader
  c.elect('B');
  c.runUntilIdle();
  assert.equal(c.leader()?.id, 'B');
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('stale prepare is nacked and candidate backs off cleanly', () => {
  const { c, mon } = makeCluster();
  // A's prepares are lost in transit; B then wins ballot (1,B).
  c.elect('A');
  c.net.dropPending();
  c.elect('B');
  c.runUntilIdle();
  assert.equal(c.leader()?.id, 'B');
  // A retries with its stale ballot (1,A) < (1,B): must be nacked.
  c.retryElect('A');
  c.runUntilIdle();
  assert.equal(c.leader()?.id, 'B');
  assert.equal(c.node('A').role, 'follower');
  assert.equal(c.node('A').pendingPromises, 0);
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('queued client values drain once the node wins leadership', () => {
  const { c, mon } = makeCluster();
  assert.equal(c.propose('C', 'early'), false);
  assert.equal(c.node('C').pendingClientValues, 1);
  c.elect('C');
  c.runUntilIdle();
  assert.equal(c.node('C').pendingClientValues, 0);
  for (const id of c.nodeIds) {
    assert.deepEqual(c.node(id).committedPrefix(), ['early']);
  }
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('higher-ballot acceptor state survives leader change (no noop clobber)', () => {
  const { c, mon } = makeCluster();
  c.elect('A');
  c.runUntilIdle();
  c.propose('A', 'kept');
  c.runUntilIdle();
  c.tick(150);
  c.elect('B');
  c.runUntilIdle();
  // 'kept' was committed; every node must still have it, no noop overwrite.
  for (const id of c.nodeIds) {
    assert.deepEqual(c.node(id).committedPrefix(), ['kept']);
    assert.notEqual(c.node(id).log.get(0)?.entry.value, NOOP_VALUE);
  }
  mon.assertClean();
  mon.dispose();
  c.dispose();
});
