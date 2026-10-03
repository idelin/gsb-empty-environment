import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { InvariantMonitor } from '../src/monitor.ts';

test('clean runs produce zero violations across many checks', () => {
  const c = new Cluster(5);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  for (let i = 0; i < 10; i++) c.propose('A', `v${i}`);
  c.runUntilIdle();
  assert.ok(mon.checksRun > 0);
  mon.assertClean();
  assert.deepEqual(mon.getViolations(), []);
  mon.dispose();
  c.dispose();
});

test('monitor catches state machine safety violation at the exact commit', () => {
  const c = new Cluster(3);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  c.propose('A', 'good');
  c.runUntilIdle();
  // The protocol itself rejects forged commits for committed slots...
  const b = c.node('B');
  (b as unknown as { receive: (m: unknown) => void }).receive({
    kind: 'commit',
    from: 'C',
    entry: {
      slot: 0,
      ballot: { term: 99, nodeId: 'C' },
      value: 'evil',
      parentSlot: -1,
    },
  });
  assert.deepEqual(b.committedPrefix(), ['good']);
  // ...so corrupt B's record directly and let the monitor catch it.
  b.log.get(0)!.entry = { ...b.log.get(0)!.entry, value: 'evil' };
  mon.checkAll();
  assert.ok(
    mon.getViolations().some((v) => v.invariant.startsWith('I3')),
    JSON.stringify(mon.getViolations()),
  );
  mon.dispose();
  c.dispose();
});

test('monitor catches duplicate leadership claim of the same ballot', () => {
  const c = new Cluster(3);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  // Forge a second leadership event for the identical ballot.
  (c as unknown as { emit: (e: unknown) => void }).emit;
  // Emit via a real second election path is impossible for one ballot, so
  // drive the monitor hook directly through a forged cluster event.
  const listeners = (
    c as unknown as { listeners: Set<(e: unknown) => void> }
  ).listeners;
  for (const fn of listeners) {
    (fn as (e: unknown) => void)({ type: 'leader', term: 1, nodeId: 'A' });
  }
  assert.ok(mon.getViolations().some((v) => v.invariant.startsWith('I1')));
  mon.dispose();
  c.dispose();
});

test('monitor catches gap-free commit violation on corrupted state', () => {
  const c = new Cluster(3);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  c.propose('A', 'only');
  c.runUntilIdle();
  // Corrupt: punch a hole into A's committed prefix.
  const a = c.node('A');
  a.log.get(0)!.committed = false;
  mon.checkAll();
  assert.ok(mon.getViolations().some((v) => v.invariant.startsWith('I4')));
  mon.dispose();
  c.dispose();
});

test('disposed monitor stops observing and leaks nothing', () => {
  const c = new Cluster(3);
  const mon = new InvariantMonitor(c);
  c.elect('A');
  c.runUntilIdle();
  mon.dispose();
  const before = mon.getViolations().length;
  c.propose('A', 'after-dispose');
  c.runUntilIdle();
  assert.equal(mon.getViolations().length, before);
  assert.equal(mon.checksRun > 0, true);
  c.dispose();
});
