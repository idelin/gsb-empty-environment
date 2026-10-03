import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/paxos/cluster.ts';

const IDS = ['N1', 'N2', 'N3', 'N4', 'N5'];

// Acceptance scenario: a 5-node cluster suffers a network partition. The old
// leader is cut off with a minority; a new leader is elected by the majority,
// completes undecided slots through phase 1, and safely overwrites the
// minority's dirty log once the partition heals.
test('network partition: majority replaces old leader and repairs the log', () => {
  const cluster = new Cluster(IDS, { leaseMs: 100 });
  const n1 = cluster.elect('N1');
  cluster.propose('v1'); // slot 1
  cluster.propose('v2'); // slot 2
  assert.equal(n1.getCommitIndex(), 2);

  // Partition: {N1,N2} (minority, old leader) | {N3,N4,N5} (majority).
  cluster.network.partition([['N1', 'N2'], ['N3', 'N4', 'N5']]);

  // The old leader still holds a valid lease and accepts a client write, but
  // it can only reach N2: slot 3 stays dirty and uncommitted on the minority.
  n1.propose('v3-dirty');
  cluster.network.flush();
  assert.equal(n1.getCommitIndex(), 2, 'minority leader cannot commit');
  assert.equal(cluster.node('N2').entry(3)?.value, 'v3-dirty');
  assert.equal(cluster.node('N4').entry(3), undefined);

  // Old leader's lease expires; it still has not heard of the new term.
  cluster.advanceClock(200);
  assert.equal(n1.hasValidLease(), false);

  // The majority side elects a new leader (term 2). Phase 1 collects
  // promises from N3,N4,N5; their tails are empty past slot 2, so the new
  // leader starts appending at slot 3 with its fresh ballot.
  const n3 = cluster.node('N3');
  n3.campaign();
  cluster.network.flush();
  assert.equal(n3.getRole(), 'leader');
  assert.equal(n3.getCurrentBallot()?.term, 2);
  assert.equal(n1.getRole(), 'leader', 'old leader is unaware across the partition');

  n3.propose('w3'); // slot 3 on the majority side
  n3.propose('w4'); // slot 4
  cluster.network.flush();
  assert.equal(n3.getCommitIndex(), 4);
  assert.equal(cluster.node('N5').entry(3)?.value, 'w3');
  assert.equal(n1.getCommitIndex(), 2, 'old leader still cannot commit');

  // Heal: the next majority-side commit propagates the authoritative log.
  cluster.network.heal();
  n3.propose('w5');
  cluster.network.flush();

  // The old leader stepped down on seeing the higher term.
  assert.equal(n1.getRole(), 'follower', 'old leader deposed after heal');
  assert.equal(cluster.node('N2').getRole(), 'follower');

  // Every replica converges to the exact same committed log; the minority's
  // dirty slot-3 value is safely overwritten by the committed one.
  for (const id of IDS) {
    const node = cluster.node(id);
    assert.equal(node.getCommitIndex(), 5, `${id} commitIndex`);
    assert.deepEqual(
      node.committedEntries().map((e) => e.value),
      ['v1', 'v2', 'w3', 'w4', 'w5'],
      `${id} converged log`,
    );
    assert.equal(node.entry(3)?.value, 'w3', `${id} dirty slot 3 overwritten`);
  }
  cluster.dispose();
});

test('new leader phase 1 adopts an uncommitted value visible to its quorum', () => {
  const cluster = new Cluster(IDS, { leaseMs: 100 });
  const n1 = cluster.elect('N1');
  cluster.propose('a'); // slot 1 committed everywhere

  // Slot 2 reaches N1,N2,N3 but the Commit only reaches N2 before the
  // partition lands: N3 holds an accepted-but-uncommitted copy.
  cluster.network.partition([['N1', 'N2', 'N3'], ['N4', 'N5']]);
  n1.propose('b');
  cluster.network.flush();
  assert.equal(n1.isCommitted(2), true);
  cluster.network.partition([['N1', 'N2'], ['N3', 'N4', 'N5']]);
  cluster.advanceClock(200);

  // N5 is elected by the majority; N3's promise carries slot 2 = 'b'.
  const n5 = cluster.node('N5');
  n5.campaign();
  cluster.network.flush();
  assert.equal(n5.getRole(), 'leader');
  assert.equal(n5.entry(2)?.value, 'b', 'phase 1 adopted the visible value');
  assert.equal(n5.getCommitIndex(), 2, 'adopted entry committed contiguously');

  cluster.network.heal();
  n5.propose('c');
  cluster.network.flush();
  for (const id of IDS) {
    assert.deepEqual(
      cluster.node(id).committedEntries().map((e) => e.value),
      ['a', 'b', 'c'],
      `${id} converged`,
    );
  }
  cluster.dispose();
});

test('isolated old leader cannot commit even after its lease is refreshed locally', () => {
  const cluster = new Cluster(IDS, { leaseMs: 1000 });
  const n1 = cluster.elect('N1');
  cluster.propose('x1');

  cluster.network.partition([['N1'], ['N2', 'N3', 'N4', 'N5']]);
  n1.propose('x2'); // valid lease, but no majority reachable
  cluster.network.flush();
  assert.equal(n1.getCommitIndex(), 1);
  assert.equal(n1.isCommitted(2), false);

  // Majority elects a replacement and moves the log on.
  const n2 = cluster.node('N2');
  n2.campaign();
  cluster.network.flush();
  n2.propose('y2');
  cluster.network.flush();
  assert.equal(n2.getCommitIndex(), 2);

  // After healing, the old leader's uncommitted entry is replaced.
  cluster.network.heal();
  n2.propose('y3');
  cluster.network.flush();
  assert.equal(n1.getRole(), 'follower');
  assert.equal(n1.entry(2)?.value, 'y2', 'dirty minority entry safely overwritten');
  assert.deepEqual(
    n1.committedEntries().map((e) => e.value),
    ['x1', 'y2', 'y3'],
  );
  cluster.dispose();
});
