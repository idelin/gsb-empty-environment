import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster, NotCommittedError } from '../src/paxos.ts';
import { assertNoLeaks, assertConsistentLogs, payloads } from './helpers.ts';

const IDS = ['n1', 'n2', 'n3', 'n4', 'n5'];

/**
 * Acceptance scenario: 5-node cluster, network partition isolates the old
 * leader with a minority. The majority elects a new leader which runs
 * Phase 1, completes uncommitted slots and safely overwrites the minority's
 * dirty log entries. After healing, every node converges.
 */
test('partition: majority new leader overwrites minority dirty log', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');

  // Old leader commits two entries with the full cluster.
  n1.propose({ kind: 'app', payload: 'A' });
  n1.propose({ kind: 'app', payload: 'B' });
  assert.equal(n1.commitIndex, 2);

  // Partition: old leader n1 keeps only n2; majority is n3,n4,n5.
  c.partition([
    ['n1', 'n2'],
    ['n3', 'n4', 'n5'],
  ]);

  // n1 still holds its lease and accepts a dirty entry on the minority
  // (n1+n2 = 2 < 3), so it can never be committed.
  assert.throws(() => n1.propose({ kind: 'app', payload: 'C-dirty' }), NotCommittedError);
  assert.equal(n1.log.get(3)?.entry.payload, 'C-dirty', 'dirty entry sits in the minority log');
  assert.equal(n1.commitIndex, 2, 'dirty entry is not committed');

  // Majority side elects a new leader (leases expire).
  c.clock.advance(1000);
  const n3 = c.node('n3');
  const slot = n3.propose({ kind: 'app', payload: 'D' });
  assert.equal(slot, 3, 'new leader reuses the uncommitted slot');
  assert.equal(n3.commitIndex, 3);
  assert.deepEqual(payloads(n3), ['A', 'B', 'D']);
  assert.deepEqual(payloads(c.node('n5')), ['A', 'B', 'D']);

  // The old leader is still partitioned and cannot disrupt the majority.
  // (Its lease has expired by now, so it cannot even gather a majority;
  // either way it must fail without committing.)
  assert.throws(() => n1.propose({ kind: 'app', payload: 'E-dirty' }));
  assert.equal(n1.commitIndex, 2);

  // Heal: the next commit broadcast makes the minority converge; the dirty
  // entry is safely overwritten by the chosen value D.
  c.heal();
  n3.propose({ kind: 'app', payload: 'E' });

  for (const id of IDS) {
    assert.deepEqual(payloads(c.node(id)), ['A', 'B', 'D', 'E'], `${id} converged`);
    assert.equal(c.node(id).log.get(3)?.entry.payload, 'D', `${id}: dirty C-dirty overwritten`);
  }
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('partition: dirty entry visible to the new majority is adopted, not lost', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');
  n1.propose({ kind: 'app', payload: 'A' });

  // n1 gets a dirty entry accepted on n1+n2 (minority), then the partition
  // moves: n1 is isolated alone and n2 carries the dirty value into the
  // new majority.
  c.partition([
    ['n1', 'n2'],
    ['n3', 'n4', 'n5'],
  ]);
  assert.throws(() => n1.propose({ kind: 'app', payload: 'C-dirty' }), NotCommittedError);
  assert.equal(c.node('n2').log.get(2)?.entry.payload, 'C-dirty');
  c.heal();
  c.partition([['n1'], ['n2', 'n3', 'n4', 'n5']]);

  c.clock.advance(1000);
  const n3 = c.node('n3');
  n3.propose({ kind: 'app', payload: 'D' });

  // Phase 1 saw C-dirty on n2 with the highest ballot at slot 2, so Paxos
  // requires the new leader to adopt it — either outcome is safe, but the
  // highest-ballot value must win.
  assert.equal(n3.log.get(2)?.entry.payload, 'C-dirty');
  assert.deepEqual(payloads(n3), ['A', 'C-dirty', 'D']);
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('partition: new leader fills holes with no-ops to keep commits contiguous', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');
  n1.propose({ kind: 'app', payload: 'A' }); // slot 1 committed

  // n1 gets slot 2 accepted on n2 only (dirty), then the partition moves.
  c.partition([['n1', 'n2'], ['n3', 'n4', 'n5']]);
  assert.throws(() => n1.propose({ kind: 'app', payload: 'holey' }), NotCommittedError);

  c.clock.advance(1000);
  const n4 = c.node('n4');
  n4.propose({ kind: 'app', payload: 'X' }); // slot 2 in the majority
  n4.propose({ kind: 'app', payload: 'Y' }); // slot 3
  assert.equal(n4.commitIndex, 3);
  assert.deepEqual(payloads(n4), ['A', 'X', 'Y']);

  c.heal();
  n4.propose({ kind: 'app', payload: 'Z' });
  for (const id of IDS) {
    assert.deepEqual(payloads(c.node(id)), ['A', 'X', 'Y', 'Z'], `${id} contiguous, hole-free`);
  }
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('partition: committed entries are never lost across leadership changes', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');
  n1.propose({ kind: 'app', payload: 'vital' });

  // Only n5 (plus the now-isolated leader) ever saw the commit.
  c.partition([
    ['n1', 'n5'],
    ['n2', 'n3', 'n4'],
  ]);
  c.clock.advance(1000);
  const n2 = c.node('n2');
  n2.propose({ kind: 'app', payload: 'after' });
  // Phase 1 must discover the committed value via quorum intersection.
  assert.deepEqual(payloads(n2), ['vital', 'after']);

  c.heal();
  n2.propose({ kind: 'app', payload: 'final' });
  for (const id of IDS) {
    assert.deepEqual(payloads(c.node(id)), ['vital', 'after', 'final']);
  }
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});
