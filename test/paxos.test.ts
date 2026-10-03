import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/paxos/cluster.ts';
import { NOOP } from '../src/paxos/types.ts';

function assertConverged(c: Cluster, expected: unknown[]): void {
  for (const node of c.nodes) {
    assert.deepEqual(
      node.applied.map((a) => a.value),
      expected,
      `node ${node.id} applied log`,
    );
    // No-gap delivery: applied slots are exactly 1..commitUpTo.
    node.applied.forEach((a, i) => assert.equal(a.slot, i + 1));
    assert.equal(node.commitUpTo, expected.length);
  }
  assert.ok(c.network.leakFree, 'network/inbox leak check');
  for (const node of c.nodes) {
    assert.equal(node.pendingValues.length, 0, `node ${node.id} pending values`);
  }
}

test('basic election and single commit to all 5 nodes', () => {
  const c = new Cluster(5);
  c.nodes[0].propose('x');
  c.pump();
  assert.ok(c.nodes[0].isLeader);
  assertConverged(c, ['x']);
});

test('leader lease skips Phase 1 for consecutive entries', () => {
  const c = new Cluster(5);
  c.nodes[0].propose('a');
  c.pump();
  const preparesAfterElection = c.network.countSent('prepare');
  assert.ok(preparesAfterElection > 0);

  c.nodes[0].propose('b');
  c.nodes[0].propose('c');
  c.pump();
  assert.equal(
    c.network.countSent('prepare'),
    preparesAfterElection,
    'no new Prepare while the lease is valid',
  );
  assertConverged(c, ['a', 'b', 'c']);
});

test('lease expiry forces a new Phase 1', () => {
  const c = new Cluster(5);
  c.nodes[0].propose('a');
  c.pump();
  const prepares = c.network.countSent('prepare');
  c.advanceTime(1_001); // past the lease
  c.nodes[0].propose('b');
  assert.ok(c.network.countSent('prepare') > prepares, 're-ran Phase 1');
  c.pump();
  assertConverged(c, ['a', 'b']);
});

test('acceptance: partition, old leader deposed, dirty minority log overwritten', () => {
  const c = new Cluster(5);
  const [n0, n1, n2] = c.nodes;

  // 1. n0 elected, slot 1 = 'A' committed everywhere.
  n0.propose('A');
  c.pump();
  assertConverged(c, ['A']);

  // 2. Partition {0,1} | {2,3,4}. Old leader keeps "working" in minority.
  c.network.partition([0, 1], [2, 3, 4]);
  n0.propose('B'); // slot 2, accepted only by {0,1}: dirty, uncommitted
  n0.propose('D'); // slot 3, same fate
  c.pump();
  assert.equal(n0.commitUpTo, 1, 'minority leader cannot commit');
  assert.equal(n1.commitUpTo, 1);
  assert.equal(n0.acceptedLog.get(2)!.value, 'B');

  // 3. Majority side elects n2 via Phase 1 and commits without the old leader.
  n2.propose('C');
  c.pump();
  assert.ok(n2.isLeader);
  assert.equal(n2.commitUpTo, 2);
  assert.deepEqual(n2.acceptedLog.get(2)!.value, 'C');
  assert.equal(n0.commitUpTo, 1, 'old leader still isolated');

  // 4. Heal. New leader replicates: dirty 'B' is safely overwritten.
  c.network.healAll();
  n2.replicate();
  c.pump();
  assert.equal(n0.acceptedLog.get(2)!.value, 'C', 'dirty minority entry overwritten');
  assert.ok(!n0.isLeader, 'old leader stepped down');
  assertConverged(c, ['A', 'C']);

  // 5. Old leader rejoins quorum; its stale accepts are rejected, and its
  //    dirty slot-3 entry is recovered only through a proper new Phase 1.
  n0.propose('E');
  c.pump();
  assertConverged(c, ['A', 'C', 'D', 'E']);
  for (const node of c.nodes) {
    const values = node.applied.map((a) => a.value);
    assert.ok(!values.includes('B'), 'dirty value B never committed anywhere');
  }
});

test('hole repair: new leader fills missing slot with a no-op', () => {
  const c = new Cluster(5);
  const [n0, n1, n2] = c.nodes;

  n0.propose('A'); // slot 1 committed everywhere
  c.pump();

  // Slot 2 ('B') reaches only node 1; slot 3 ('D') reaches only node 2.
  c.network.setLink(0, 2, false);
  c.network.setLink(0, 3, false);
  c.network.setLink(0, 4, false);
  n0.propose('B');
  c.pump();
  c.network.setLink(0, 2, true);
  c.network.setLink(0, 1, false);
  n0.propose('D');
  c.pump();
  assert.equal(n0.commitUpTo, 1, 'neither B nor D reached a majority');
  assert.equal(n1.acceptedLog.get(2)!.value, 'B');
  assert.equal(n2.acceptedLog.get(3)!.value, 'D');
  assert.equal(n2.acceptedLog.has(2), false, 'node 2 has a hole at slot 2');

  // Partition {0,1} | {2,3,4}; n2 becomes leader. Its Phase 1 sees slot 3
  // but not slot 2, so slot 2 is repaired with a no-op.
  c.network.partition([0, 1], [2, 3, 4]);
  n2.propose('C');
  c.pump();
  assert.ok(n2.isLeader);
  assert.equal(n2.acceptedLog.get(2)!.value, NOOP, 'hole filled with no-op');
  assert.equal(n2.acceptedLog.get(3)!.value, 'D', 'highest-ballot value preserved');
  assert.equal(n2.commitUpTo, 4, 'contiguous commit over repaired log');

  // Heal: everyone converges to the repaired log, no gaps.
  c.network.healAll();
  n2.replicate();
  c.pump();
  assertConverged(c, ['A', NOOP, 'D', 'C']);
  assert.equal(n0.acceptedLog.get(2)!.value, NOOP, 'dirty B replaced by no-op');
});

test('commit is never chosen twice: conflicting minority writes lose', () => {
  const c = new Cluster(5);
  const [n0, , n2] = c.nodes;
  n0.propose('committed-value');
  c.pump();

  c.network.partition([0, 1], [2, 3, 4]);
  n0.propose('minority-value');
  c.pump();
  n2.propose('majority-value');
  c.pump();
  c.network.healAll();
  n2.replicate();
  c.pump();

  for (const node of c.nodes) {
    assert.equal(node.acceptedLog.get(1)!.value, 'committed-value');
    assert.equal(node.acceptedLog.get(2)!.value, 'majority-value');
  }
  assertConverged(c, ['committed-value', 'majority-value']);
});

test('inbox is a real lock-free queue: bounded, FIFO, drained without loss', () => {
  const c = new Cluster(5);
  c.nodes[0].propose('m');
  // Before pumping, messages sit in the receivers' lock-free inboxes.
  const queued = c.nodes.slice(1).reduce((s, n) => s + n.inbox.size(), 0);
  assert.ok(queued > 0, 'messages were enqueued, not delivered synchronously');
  c.pump();
  for (const node of c.nodes) {
    assert.ok(node.inbox.isEmpty());
    assert.ok(node.inboxLeakFree);
  }
  assert.ok(c.network.leakFree);
});
