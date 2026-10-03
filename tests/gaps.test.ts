import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { InvariantMonitor } from '../src/monitor.ts';
import { inspect } from '../src/inspector.ts';
import { NOOP_VALUE } from '../src/types.ts';

/**
 * Hole repair: the acceptor state visible to the new leader's Phase 1 has
 * a gap (slot 2 exists on nobody, slots 1 and 3 exist on a minority of
 * acceptors). The new leader must adopt the highest-ballot values for the
 * open slots, fill the hole with a no-op, and commit a contiguous prefix.
 */
test('new leader fills holes with no-ops and adopts stray accepts', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();

  c.propose('A', 'v0');
  c.runUntilIdle(); // slot 0 committed everywhere

  // Craft acceptor state: B holds v1@1; B and C hold v3@3; nobody holds
  // slot 2. A holds everything locally but will be partitioned away.
  c.propose('A', 'v1');
  c.net.deliverNext(); // v1 accept -> B only
  c.net.dropPending();
  c.propose('A', 'v2');
  c.net.dropPending(); // v2 accept -> nobody
  c.propose('A', 'v3');
  c.net.deliverNext(); // v3 accept -> B
  c.net.deliverNext(); // v3 accept -> C
  c.net.dropPending();
  assert.equal(c.node('A').commitIndex, 0);

  // Strand A (its full private log must not dominate the repair).
  c.partition([['A'], ['B', 'C', 'D', 'E']]);
  c.elect('C');
  c.runUntilIdle();

  const leader = c.node('C');
  assert.equal(leader.role, 'leader');
  assert.equal(leader.commitIndex, 3, 'contiguous commit after repair');
  assert.equal(leader.log.get(1)?.entry.value, 'v1');
  assert.equal(leader.log.get(2)?.entry.value, NOOP_VALUE);
  assert.equal(leader.log.get(3)?.entry.value, 'v3');

  // Heal: A's dirty private v2 must be overwritten by the committed no-op.
  c.heal();
  c.tick(60);
  c.runUntilIdle();
  for (const id of c.nodeIds) {
    assert.deepEqual(
      c.node(id).committedPrefix(),
      ['v0', 'v1', NOOP_VALUE, 'v3'],
      `${id} prefix`,
    );
  }
  mon.assertClean();
  const report = inspect(c.snapshot());
  assert.ok(report.ok, JSON.stringify(report.invariantViolations));
  assert.deepEqual(c.findLeaks(), []);
  mon.dispose();
  c.dispose();
});

test('commit notices arriving out of order still commit contiguously', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  c.propose('A', 's0');
  c.propose('A', 's1');
  c.runUntilIdle();
  // Everything settles; commitIndex advanced in order on every node.
  for (const id of c.nodeIds) {
    const n = c.node(id);
    assert.equal(n.commitIndex, 1);
    assert.equal(n.uncommittedChosen, 0);
  }
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('follower missing a hole is healed by commit notices after resync', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  // E misses slot 0 entirely (accept + commit dropped).
  c.propose('A', 'x0');
  c.net.dropPending();
  c.partition([['E'], ['A', 'B', 'C', 'D']]);
  // Re-propose within the majority partition: A still leads there.
  c.elect('B');
  c.runUntilIdle();
  assert.equal(c.node('B').role, 'leader');
  c.propose('B', 'x1');
  c.runUntilIdle();
  c.heal();
  c.tick(60);
  c.runUntilIdle();
  // E catches up via heartbeat + sync-request and commits contiguously.
  const e = c.node('E');
  assert.equal(e.commitIndex, c.node('B').commitIndex);
  assert.deepEqual(e.committedPrefix(), c.node('B').committedPrefix());
  mon.assertClean();
  mon.dispose();
  c.dispose();
});
