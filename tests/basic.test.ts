import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import type { Command } from '../src/types.ts';

const set = (key: string, value: string): Command => ({ kind: 'set', key, value });

test('election: candidate wins majority promises and obtains a lease', () => {
  const c = new Cluster(5);
  c.elect(0);
  assert.equal(c.nodes[0].getRole(), 'leader');
  assert.ok(c.nodes[0].getLeaseExpiry() > c.network.now);
  // Acceptors promised the leader's ballot.
  for (const node of c.nodes) {
    assert.equal(node.getPromisedFloor().term, 1);
  }
  c.assertConverged();
});

test('commit: entries commit contiguously and apply to the state machine on all nodes', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.submit(0, set('a', '1'));
  c.submit(0, set('b', '2'));
  c.submit(0, set('c', '3'));
  c.stabilize();

  for (const node of c.nodes) {
    assert.equal(node.getCommitIndex(), 3);
    assert.deepEqual(
      node.getCommittedLog().map((e) => e.slot),
      [1, 2, 3],
      'committed prefix must be hole-free',
    );
    assert.equal(node.getState().get('a'), '1');
    assert.equal(node.getState().get('b'), '2');
    assert.equal(node.getState().get('c'), '3');
  }
  c.assertConverged();
});

test('leader lease: valid lease bypasses Phase 1 entirely', () => {
  const c = new Cluster(5);
  c.elect(0);
  const since = c.sentLog.length;

  c.submit(0, set('x', '1'));
  c.submit(0, set('y', '2'));
  c.submit(0, set('z', '3'));
  c.stabilize();

  assert.equal(c.countMessages('Prepare', since), 0, 'no Phase 1 under a valid lease');
  assert.ok(c.countMessages('Accept', since) > 0, 'Phase 2 still runs');
  assert.equal(c.nodes[4].getCommitIndex(), 3);
  c.assertConverged();
});

test('lease expiry: leader re-runs Phase 1 and queued commands commit in order', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.submit(0, set('first', '1'));
  c.stabilize();

  c.network.advance(1000); // expire the lease (default duration 50)
  const since = c.sentLog.length;
  const r1 = c.submit(0, set('second', '2'));
  const r2 = c.submit(0, set('third', '3'));
  assert.equal(r1.status, 'queued');
  assert.equal(r2.status, 'queued');
  assert.ok(c.countMessages('Prepare', since) > 0, 'expired lease forces a fresh Phase 1');

  c.stabilize();
  assert.equal(c.nodes[0].getRole(), 'leader');
  for (const node of c.nodes) {
    assert.deepEqual(
      node.getCommittedLog().map((e) => e.command),
      [set('first', '1'), set('second', '2'), set('third', '3')],
    );
  }
  c.assertConverged();
});

test('submit to a follower is rejected with not-leader', () => {
  const c = new Cluster(5);
  c.elect(0);
  const res = c.submit(3, set('k', 'v'));
  assert.equal(res.status, 'not-leader');
  c.stabilize();
  assert.equal(c.nodes[3].getCommitIndex(), 0);
});

test('commitIndex never advances across an unchosen slot', () => {
  const c = new Cluster(5);
  c.elect(0);
  // Partition so slot 1 is accepted by a minority only, then a later slot
  // gets majority acceptance: commitIndex must still stop at the hole.
  c.submit(0, set('s1', 'v1'));
  c.stabilize();
  c.partition([0, 1], [2, 3, 4]);
  c.submit(0, set('s2', 'minority')); // slot 2: only nodes 0,1 accept
  c.stabilize();
  assert.equal(c.nodes[0].getCommitIndex(), 1, 'minority-accepted entry must not commit');
  c.heal();
  c.assertConverged();
});
