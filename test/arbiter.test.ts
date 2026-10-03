import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AuditTrail } from '../src/auditTrail.ts';
import { LeaseArbiter } from '../src/arbiter.ts';
import { LeaseNode } from '../src/leaseNode.ts';

function setup() {
  const trail = new AuditTrail();
  const arbiter = new LeaseArbiter(trail, { contentionWindowMs: 1 });
  return { trail, arbiter, a: new LeaseNode('A'), b: new LeaseNode('B') };
}

test('grant then reject while lock is held', () => {
  const { trail, arbiter, a, b } = setup();
  a.send(arbiter, 'acquire', 'L', 100, { ttlMs: 1000 });
  b.send(arbiter, 'acquire', 'L', 500, { ttlMs: 1000 }); // outside contention window
  const [grant, reject] = arbiter.drain();
  assert.equal(grant!.ok, true);
  assert.equal(reject!.ok, false);
  assert.equal(reject!.event.type, 'LEASE_REJECTED');
  assert.equal(reject!.event.details.reason, 'LOCK_HELD');
  assert.equal(reject!.event.details.holder, 'A');
  assert.equal(arbiter.lockState('L').state, 'LOCKED');
  assert.equal(trail.byType('CONTENTION_DETECTED').length, 0);
});

test('renew extends expiry; non-holder renew is rejected', () => {
  const { trail, arbiter, a, b } = setup();
  a.send(arbiter, 'acquire', 'L', 0, { ttlMs: 100 });
  arbiter.drain();
  b.send(arbiter, 'renew', 'L', 50, { ttlMs: 100 });
  const [badRenew] = arbiter.drain();
  assert.equal(badRenew!.ok, false);
  assert.equal(badRenew!.event.details.reason, 'NOT_HOLDER');
  a.send(arbiter, 'renew', 'L', 60, { ttlMs: 200 });
  const [goodRenew] = arbiter.drain();
  assert.equal(goodRenew!.ok, true);
  assert.equal(goodRenew!.event.details.expiresAtMs, 260);
  assert.equal(trail.byType('LEASE_RENEWED').length, 1);
});

test('transfer moves the lease; old holder can no longer renew', () => {
  const { trail, arbiter, a, b } = setup();
  a.send(arbiter, 'acquire', 'L', 0, { ttlMs: 1000 });
  arbiter.drain();
  a.send(arbiter, 'transfer', 'L', 10, { toNodeId: 'B' });
  const [transfer] = arbiter.drain();
  assert.equal(transfer!.ok, true);
  assert.equal(arbiter.lockState('L').holder, 'B');
  a.send(arbiter, 'renew', 'L', 20, { ttlMs: 100 });
  const [staleRenew] = arbiter.drain();
  assert.equal(staleRenew!.ok, false);
  assert.equal(staleRenew!.event.details.reason, 'NOT_HOLDER');
  b.send(arbiter, 'renew', 'L', 20, { ttlMs: 100 });
  assert.equal(arbiter.drain()[0]!.ok, true);
  assert.equal(trail.byType('LEASE_TRANSFERRED').length, 1);
});

test('lease expires after TTL and can be re-acquired', () => {
  const { trail, arbiter, a, b } = setup();
  a.send(arbiter, 'acquire', 'L', 0, { ttlMs: 100 });
  arbiter.drain();
  const expired = arbiter.expireDue(150);
  assert.equal(expired.length, 1);
  assert.equal(expired[0]!.type, 'LEASE_EXPIRED');
  assert.equal(arbiter.lockState('L').state, 'UNLOCKED');
  // expiry produced a LOCKED -> UNLOCKED transition
  const transitions = trail.byType('LOCK_STATE_CHANGED');
  assert.deepEqual(
    transitions.map((t) => `${t.details.from}->${t.details.to}`),
    ['UNLOCKED->LOCKED', 'LOCKED->UNLOCKED'],
  );
  b.send(arbiter, 'acquire', 'L', 200, { ttlMs: 100 });
  assert.equal(arbiter.drain()[0]!.ok, true);
  assert.equal(arbiter.lockState('L').holder, 'B');
});

test('release frees the lock', () => {
  const { arbiter, a } = setup();
  a.send(arbiter, 'acquire', 'L', 0, { ttlMs: 1000 });
  arbiter.drain();
  a.send(arbiter, 'release', 'L', 10);
  const [release] = arbiter.drain();
  assert.equal(release!.ok, true);
  assert.equal(arbiter.lockState('L').state, 'UNLOCKED');
});

test('lamport timestamps are monotonic across the trail', () => {
  const { trail, arbiter, a, b } = setup();
  a.send(arbiter, 'acquire', 'L', 0, { ttlMs: 1000 });
  b.send(arbiter, 'acquire', 'L', 0.5, { ttlMs: 1000 });
  arbiter.drain();
  const lamports = trail.all().map((e) => e.lamport);
  for (let i = 1; i < lamports.length; i++) {
    assert.ok(lamports[i]! > lamports[i - 1]!, `lamport[${i}] must increase`);
  }
});
