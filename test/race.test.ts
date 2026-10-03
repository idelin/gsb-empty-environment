import { test } from 'node:test';
import assert from 'node:assert/strict';
import { simulateMasterLeaseRace } from '../src/raceSimulation.ts';

/**
 * Acceptance boundary: two nodes contend for the master lease within the
 * same millisecond; the audit trail must reconstruct arrival order, lock
 * state transitions, the rejection, and a causal evidence chain.
 */
test('millisecond race: full reconstruction from the audit trail', () => {
  const race = simulateMasterLeaseRace({
    nodeA: 'node-A',
    nodeB: 'node-B',
    leaseId: 'master',
    baseMs: 10_000,
    gapMs: 0.42,
    ttlMs: 5_000,
  });

  // 1. Both competing requests were recorded, in true arrival order.
  const [reqA, reqB] = race.requestEvents;
  assert.equal(reqA.nodeId, 'node-A');
  assert.equal(reqB.nodeId, 'node-B');
  assert.ok(reqB.wallClockMs - reqA.wallClockMs < 1, 'race window is sub-millisecond');
  const requests = race.trail.byType('LEASE_REQUESTED');
  assert.deepEqual(requests.map((r) => r.nodeId), ['node-A', 'node-B']);

  // 2. The race itself is flagged as contention with the arrival gap.
  const contention = race.contentionEvent!;
  assert.equal(contention.type, 'CONTENTION_DETECTED');
  assert.deepEqual(contention.details.contenders, ['node-A', 'node-B']);
  assert.ok(Math.abs((contention.details.gapMs as number) - 0.42) < 1e-9);

  // 3. Arbiter lock state machine: UNLOCKED -> LOCKED exactly once.
  assert.equal(race.lockTransitions.length, 1);
  assert.equal(race.lockTransitions[0]!.details.from, 'UNLOCKED');
  assert.equal(race.lockTransitions[0]!.details.to, 'LOCKED');
  assert.equal(race.lockTransitions[0]!.details.holder, 'node-A');

  // 4. First arrival wins, second is rejected with LOCK_HELD.
  assert.equal(race.winner, 'node-A');
  assert.equal(race.loser, 'node-B');
  assert.equal(race.rejectionEvent.details.reason, 'LOCK_HELD');
  assert.equal(race.rejectionEvent.details.holder, 'node-A');

  // 5. Causal evidence chain: the rejection is provably caused by the
  //    loser's own request AND the winner's grant (which in turn was
  //    caused by the winner's request).
  const chain = race.evidenceChain;
  const chainIds = chain.map((e) => e.id);
  assert.ok(chainIds.includes(reqB.id), 'chain contains loser request');
  assert.ok(chainIds.includes(reqA.id), 'chain contains winner request');
  assert.ok(chainIds.includes(race.grantEvent.id), 'chain contains winner grant');
  assert.equal(chain[chain.length - 1]!.id, race.rejectionEvent.id);
  assert.ok(race.trail.isCausalAncestor(race.grantEvent.id, race.rejectionEvent.id));
  assert.ok(race.trail.isCausalAncestor(reqA.id, race.rejectionEvent.id));

  // 6. The grant happened-before the rejection in logical time.
  assert.ok(race.grantEvent.lamport < race.rejectionEvent.lamport);
});

test('race outcome is deterministic: arrival order decides the winner', () => {
  const first = simulateMasterLeaseRace({ gapMs: 0.1 });
  const second = simulateMasterLeaseRace({ gapMs: 0.9 });
  assert.equal(first.winner, 'node-A');
  assert.equal(second.winner, 'node-A');
  assert.equal(first.loser, 'node-B');
  assert.equal(second.loser, 'node-B');
});

test('requests outside the contention window do not raise a race flag', () => {
  const race = simulateMasterLeaseRace({ gapMs: 50, contentionWindowMs: 1 });
  assert.equal(race.trail.byType('CONTENTION_DETECTED').length, 0);
  // ...but the lock is still held, so the loser is still rejected.
  assert.equal(race.rejectionEvent.details.reason, 'LOCK_HELD');
});
