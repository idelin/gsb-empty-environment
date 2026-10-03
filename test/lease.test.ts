import test from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { LEASE_DURATION } from '../src/node.ts';
import { assertNoLeaks} from './helpers.ts';

test('lease: valid lease bypasses Phase 1 for consecutive slots', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  const prepareAfterElection = c.stats.Prepare;
  assert.equal(c.propose(0, 'v1').slot, 1);
  assert.equal(c.propose(0, 'v2').slot, 2);
  assert.equal(c.propose(0, 'v3').slot, 3);
  c.run();
  assert.equal(c.stats.Prepare, prepareAfterElection, 'no extra Prepare while the lease is valid');
  assert.equal(c.stats.Accept, 15, '3 values x 5 nodes, Phase 2 only');
  for (const n of c.nodes) {
    assert.deepEqual(n.committedValues(), ['v1', 'v2', 'v3']);
  }
  assertNoLeaks(c);
});

test('lease: expired lease forces a fresh Phase 1 before proposing', () => {
  const c = new Cluster(5);
  c.elect(0);
  c.run();
  assert.equal(c.propose(0, 'v1').ok, true);
  c.run();
  const prepareBefore = c.stats.Prepare;

  c.wait(LEASE_DURATION + 10); // move the clock past the lease
  c.run();
  const r = c.propose(0, 'v2');
  assert.equal(r.ok, true);
  assert.equal(r.deferred, true, 'proposal is queued behind a re-election');
  assert.ok(c.stats.Prepare > prepareBefore, 're-election ran Phase 1');
  c.run();
  for (const n of c.nodes) {
    assert.deepEqual(n.committedValues(), ['v1', 'v2']);
  }
  assertNoLeaks(c);
});

test('lease: boundary — valid one tick before, expired exactly at leaseUntil', () => {
  // One tick before expiry: the fast path still applies.
  const before = new Cluster(5);
  before.elect(0);
  before.run();
  const leaseUntil = before.nodes[0].leaseUntil;
  assert.equal(leaseUntil, before.now + LEASE_DURATION);
  before.wait(leaseUntil - 1 - before.now);
  before.run();
  assert.equal(before.now, leaseUntil - 1);
  assert.equal(before.propose(0, 'in-lease').deferred, undefined);
  before.run();
  assert.deepEqual(before.nodes[3].committedValues(), ['in-lease']);
  assertNoLeaks(before);

  // Exactly at expiry: the lease is void, Phase 1 must run again.
  const at = new Cluster(5);
  at.elect(0);
  at.run();
  const until = at.nodes[0].leaseUntil;
  at.wait(until - at.now);
  at.run();
  assert.equal(at.now, until);
  assert.equal(at.propose(0, 'post-lease').deferred, true, 'expired exactly at the boundary');
  at.run();
  for (const n of at.nodes) {
    assert.deepEqual(n.committedValues(), ['post-lease']);
  }
  assertNoLeaks(at);
});

test('lease: re-election after expiry preserves every committed entry', () => {
  const c = new Cluster(5);
  c.elect(1);
  c.run();
  c.propose(1, 'a');
  c.propose(1, 'b');
  c.run();
  c.wait(LEASE_DURATION * 3);
  c.run();
  c.propose(1, 'c'); // triggers re-election through an expired lease
  c.run();
  for (const n of c.nodes) {
    assert.deepEqual(n.committedValues(), ['a', 'b', 'c']);
  }
  assertNoLeaks(c);
});
