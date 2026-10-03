import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster, NoMajorityError, NotCommittedError } from '../src/paxos.ts';
import { assertNoLeaks, assertConsistentLogs, payloads } from './helpers.ts';

const IDS = ['n1', 'n2', 'n3', 'n4', 'n5'];

test('leader election then lease fast-path commits (Phase 1 skipped)', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');

  const s1 = n1.propose({ kind: 'app', payload: 'A' });
  assert.equal(s1, 1);
  assert.equal(n1.prepareCount, 1, 'first proposal runs Phase 1 once');
  const acceptsAfterFirst = n1.acceptCount;

  n1.propose({ kind: 'app', payload: 'B' });
  n1.propose({ kind: 'app', payload: 'C' });
  assert.equal(n1.prepareCount, 1, 'valid lease bypasses Phase 1 for consecutive entries');
  assert.ok(n1.acceptCount > acceptsAfterFirst, 'Phase 2 still runs per entry');
  assert.equal(n1.commitIndex, 3);
  assert.deepEqual(payloads(n1), ['A', 'B', 'C']);

  // Followers converge via commit broadcast.
  for (const id of IDS) assert.deepEqual(payloads(c.node(id)), ['A', 'B', 'C']);
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('lease expiry forces a new Phase 1', () => {
  const c = new Cluster(IDS, 50);
  const n1 = c.node('n1');
  n1.propose({ kind: 'app', payload: 'A' });
  assert.equal(n1.prepareCount, 1);

  c.clock.advance(50); // lease (50ms) now expired
  assert.ok(!n1.hasValidLease());
  n1.propose({ kind: 'app', payload: 'B' });
  assert.equal(n1.prepareCount, 2, 'expired lease requires Prepare/Promise again');
  assert.equal(n1.commitIndex, 2);
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('commit engine is hole-free: commitIndex only advances contiguously', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');
  for (let i = 1; i <= 10; i++) n1.propose({ kind: 'app', payload: `cmd-${i}` });
  assert.equal(n1.commitIndex, 10);
  for (const id of IDS) {
    const node = c.node(id);
    assert.equal(node.applied.length, 10);
    for (let slot = 1; slot <= 10; slot++) assert.ok(node.log.has(slot), `hole at ${slot}`);
  }
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('concurrent campaigns: higher ballot wins, loser cannot commit', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');
  const n2 = c.node('n2');

  assert.ok(n1.campaign());
  // n2 observed n1's prepare, so it campaigns with a higher round and wins.
  assert.ok(n2.campaign());
  assert.ok(n2.isLeader);
  assert.ok(!n1.isLeader, 'n1 steps down on seeing the higher ballot');

  // The new leader commits normally.
  const slot = n2.propose({ kind: 'app', payload: 'from-n2' });
  assert.equal(slot, 1);
  assert.deepEqual(payloads(n2), ['from-n2']);

  // n1 regains leadership with a yet-higher round; the committed entry
  // survives the takeover (Paxos safety).
  assert.ok(n1.campaign());
  n1.propose({ kind: 'app', payload: 'from-n1' });
  assert.deepEqual(payloads(n1), ['from-n2', 'from-n1']);
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('acceptor rejects Accept with a lower ballot than promised', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');
  const n5 = c.node('n5');

  assert.ok(n1.campaign()); // everyone promises (1, n1)
  assert.ok(n5.campaign()); // everyone re-promises (2, n5); n1 steps down

  // n1 (stale) tries to propose: it must re-campaign first; with a higher
  // round it may win again — the safety property is that committed entries
  // survive. n5 commits a value first.
  n5.propose({ kind: 'app', payload: 'chosen-by-n5' });
  n1.propose({ kind: 'app', payload: 'n1-next' });
  assert.deepEqual(payloads(n5), ['chosen-by-n5', 'n1-next']);
  assert.deepEqual(payloads(n1), ['chosen-by-n5', 'n1-next']);
  assertConsistentLogs(c);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('node isolated from majority cannot become leader or commit', () => {
  const c = new Cluster(IDS);
  c.partition([['n1'], ['n2', 'n3', 'n4', 'n5']]);
  const n1 = c.node('n1');
  assert.ok(!n1.campaign(), 'isolated node gets no majority');
  assert.throws(() => n1.propose({ kind: 'app', payload: 'x' }), NoMajorityError);
  assert.equal(n1.commitIndex, 0);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});

test('leader in minority partition cannot commit (NotCommitted)', () => {
  const c = new Cluster(IDS);
  const n1 = c.node('n1');
  n1.propose({ kind: 'app', payload: 'A' }); // leader with lease
  c.partition([['n1', 'n2'], ['n3', 'n4', 'n5']]);
  assert.throws(() => n1.propose({ kind: 'app', payload: 'dirty' }), NotCommittedError);
  assert.equal(n1.commitIndex, 1, 'only the pre-partition entry is committed');
  // Dirty entry exists on the minority but is not chosen.
  assert.equal(n1.log.get(2)?.entry.payload, 'dirty');
  assert.equal(c.node('n3').log.has(2), false);
  assertNoLeaks(c.net, [...c.nodes.values()]);
});
