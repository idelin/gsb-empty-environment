import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { InvariantMonitor } from '../src/monitor.ts';

test('no leaks after a full partition-heal lifecycle', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  c.propose('A', 'one');
  c.runUntilIdle();
  c.partition([['A', 'B'], ['C', 'D', 'E']]);
  c.propose('A', 'dirty');
  c.elect('C');
  c.runUntilIdle();
  c.propose('C', 'two');
  c.runUntilIdle();
  c.heal();
  c.tick(60);
  c.runUntilIdle();
  mon.assertClean();
  assert.deepEqual(c.findLeaks(), []);
  assert.equal(c.net.pending, 0);
  mon.dispose();
  c.dispose();
  assert.deepEqual(c.findLeaks(), []);
});

test('no leaks after election timeout cleanup', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  // Doomed candidacy: prepares dropped, election must time out cleanly.
  c.elect('B');
  c.net.dropPending();
  c.tick(200); // exceeds election deadline
  c.elect('A'); // may be nacked by B's stale self-promise
  c.runUntilIdle();
  c.elect('A'); // retry with the learned term: wins
  c.runUntilIdle();
  assert.equal(c.node('A').role, 'leader');
  c.propose('A', 'x');
  c.runUntilIdle();
  mon.assertClean();
  assert.deepEqual(c.findLeaks(), []);
  mon.dispose();
  c.dispose();
});

test('dispose removes every listener and hook', () => {
  const c = new Cluster(3);
  const mon = new InvariantMonitor(c);
  const off = c.onEvent(() => {});
  off();
  c.elect('A');
  c.runUntilIdle();
  mon.dispose();
  c.dispose();
  // Cluster is inert: further events have no observers, network is empty.
  assert.equal((c as unknown as { listeners: Set<unknown> }).listeners.size, 0);
  assert.equal(c.net.pending, 0);
  assert.deepEqual(c.findLeaks(), []);
});

test('message accounting balances: delivered + dropped == sent - pending', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  c.partition([['A'], ['B', 'C', 'D', 'E']]);
  c.propose('A', 'lost');
  c.runUntilIdle();
  c.elect('B');
  c.runUntilIdle();
  c.heal();
  c.tick(60);
  c.runUntilIdle();
  const n = c.net;
  assert.equal(n.delivered + n.droppedByPartition + n.pending, n.sent);
  mon.assertClean();
  mon.dispose();
  c.dispose();
});

test('repeated leader churn leaves no residual protocol state', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  for (let round = 0; round < 6; round++) {
    const id = c.nodeIds[round % c.nodeIds.length];
    c.elect(id);
    c.runUntilIdle();
    c.propose(id, `round-${round}`);
    c.runUntilIdle();
    c.tick(150); // expire the lease between rounds
  }
  // Final leader + sync so all nodes converge.
  c.elect('A');
  c.runUntilIdle();
  c.tick(60);
  c.runUntilIdle();
  mon.assertClean();
  const prefixes = c.nodeIds.map((id) => c.node(id).committedPrefix());
  for (const p of prefixes) assert.deepEqual(p, prefixes[0]);
  assert.deepEqual(c.findLeaks(), []);
  mon.dispose();
  c.dispose();
});
