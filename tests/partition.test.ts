import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { InvariantMonitor } from '../src/monitor.ts';
import { inspect } from '../src/inspector.ts';

/**
 * Acceptance boundary: 5-node cluster, network partition strands the old
 * leader with a minority; a majority new leader takes over, completes the
 * uncommitted slots via Phase 1 and safely overwrites the minority's dirty
 * log records after healing.
 */
test('partition: old leader replaced, slots repaired, dirty logs overwritten', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);

  // 1. A becomes leader and commits one entry on all nodes.
  c.elect('A');
  c.runUntilIdle();
  c.propose('A', 'committed-0');
  c.runUntilIdle();
  for (const id of c.nodeIds) {
    assert.deepEqual(c.node(id).committedPrefix(), ['committed-0']);
  }

  // 2. Partition: {A,B} vs {C,D,E}. A proposes two more entries that can
  //    only reach B -> dirty minority records, never committed.
  c.partition([['A', 'B'], ['C', 'D', 'E']]);
  c.propose('A', 'dirty-1');
  c.propose('A', 'dirty-2');
  c.runUntilIdle();
  assert.equal(c.node('A').commitIndex, 0); // cannot commit without majority
  assert.equal(c.node('B').log.get(1)?.entry.value, 'dirty-1');
  assert.equal(c.node('C').log.get(1), undefined); // majority never saw it

  // 3. C wins leadership in the majority partition. Phase 1 sees only the
  //    majority's state (slot 0 committed); the minority's dirty slots are
  //    invisible and irrelevant. C then commits fresh entries under its
  //    lease (Phase 1 bypass) into the same slots the minority dirtied.
  c.elect('C');
  c.runUntilIdle();
  assert.equal(c.node('C').role, 'leader');
  assert.equal(c.node('C').commitIndex, 0);
  c.propose('C', 'real-1');
  c.propose('C', 'real-2');
  c.runUntilIdle();
  assert.equal(c.node('C').commitIndex, 2);
  assert.equal(c.node('E').committedPrefix().length, 3);

  // 5. Old leader A still believes it leads (lease clock frozen), but its
  //    term is stale. Heal: A must step down on C's higher-ballot traffic
  //    and its dirty records must be safely overwritten by committed truth.
  c.heal();
  c.tick(60); // heartbeat round: sync-requests catch the minority up
  c.runUntilIdle();
  assert.equal(c.node('A').role, 'follower');
  for (const id of c.nodeIds) {
    const n = c.node(id);
    assert.equal(n.commitIndex, 2, `${id} commitIndex`);
    assert.deepEqual(n.committedPrefix(), [
      'committed-0',
      'real-1',
      'real-2',
    ]);
  }
  // Dirty values are gone everywhere, including the minority nodes.
  for (const id of c.nodeIds) {
    const values = [...c.node(id).log.values()].map((r) => r.entry.value);
    assert.ok(!values.includes('dirty-1'), `${id} dirty-1 purged`);
    assert.ok(!values.includes('dirty-2'), `${id} dirty-2 purged`);
  }

  // 6. No invariant was ever violated; full-state inspection is clean.
  mon.assertClean();
  const report = inspect(c.snapshot());
  assert.ok(report.ok, JSON.stringify(report, null, 2));
  assert.deepEqual(c.findLeaks(), []);
  mon.dispose();
  c.dispose();
});

test('committed value survives across the partition (never re-proposed)', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  c.propose('A', 'safe-0');
  c.propose('A', 'safe-1');
  c.runUntilIdle();
  assert.equal(c.node('A').commitIndex, 1);

  // Partition before commit notices fully propagate is not needed: entries
  // are already majority-committed. New leader must adopt, not clobber.
  c.partition([['A'], ['B', 'C', 'D', 'E']]);
  c.elect('D');
  c.runUntilIdle();
  assert.equal(c.node('D').role, 'leader');
  c.heal();
  c.tick(60);
  c.runUntilIdle();
  for (const id of c.nodeIds) {
    assert.deepEqual(c.node(id).committedPrefix(), ['safe-0', 'safe-1']);
  }
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('old leader cannot commit after losing the majority', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  c.partition([['A', 'B'], ['C', 'D', 'E']]);
  c.propose('A', 'doomed');
  c.runUntilIdle();
  assert.equal(c.node('A').commitIndex, -1);
  c.elect('E');
  c.runUntilIdle();
  assert.equal(c.node('E').role, 'leader');
  c.heal();
  c.tick(60);
  c.runUntilIdle();
  // 'doomed' was never majority-accepted: it must not appear committed.
  for (const id of c.nodeIds) {
    assert.ok(!c.node(id).committedPrefix().includes('doomed'));
  }
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('split brain attempt: two simultaneous elections, higher ballot wins', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.partition([['A', 'B'], ['C', 'D', 'E']]);
  c.elect('A'); // minority: stuck
  c.elect('C'); // majority: wins
  c.runUntilIdle();
  assert.equal(c.node('C').role, 'leader');
  assert.equal(c.node('A').role, 'follower');
  c.propose('C', 'winner');
  c.runUntilIdle();
  c.heal();
  c.tick(60);
  c.runUntilIdle();
  for (const id of c.nodeIds) {
    assert.deepEqual(c.node(id).committedPrefix(), ['winner']);
  }
  mon.assertClean();
  mon.dispose();
  c.dispose();
});
