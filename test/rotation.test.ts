import assert from 'node:assert/strict';
import test from 'node:test';
import { makeRevocation, makeRotation } from '../src/certs.ts';
import { Chain, feedAll, makeFixture, shuffled } from './helpers.ts';

test('rotation on one conflict branch does not endorse the other branch', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const base = Array.from({ length: 5 }, (_, i) => chain.next(`base${i}`));
  const branchA = chain.branch();
  const branchB = chain.branch();
  const a5 = branchA.next('a5');
  const b5 = branchB.next('b5');
  const rotation = makeRotation(fx.author, 0, 1, fx.gen1, a5.digest, fx.gen0);
  assert.equal(fx.host.ingestRotation(rotation).accepted, true);
  const a6 = branchA.next('a6', fx.gen1, 1);
  const a7 = branchA.next('a7', fx.gen1, 1);
  const b6 = branchB.next('b6', fx.gen1, 1);
  feedAll(fx.host, shuffled([...base, a5, b5, a6, a7, b6], 5));
  const view = fx.host.view(fx.author);
  const reasonOf = (digest: string) => view.quarantined.find((q) => q.digest === digest)?.reason;
  assert.equal(reasonOf(b6.digest), 'rotation-scope');
  assert.equal(reasonOf(a6.digest), 'fork-conflict');
  assert.equal(reasonOf(a7.digest), 'fork-conflict');
});

test('new-generation record arriving before its rotation cert stays pending', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const r0 = chain.next('r0');
  const rotation = makeRotation(fx.author, 0, 1, fx.gen1, r0.digest, fx.gen0);
  const r1 = chain.next('r1', fx.gen1, 1);
  feedAll(fx.host, [r0]);
  const early = fx.host.ingestRecord(r1);
  assert.equal(early.accepted, false);
  assert.equal(early.reason, 'key-unknown-pending');
  assert.equal(fx.host.view(fx.author).heads.length, 1);
  assert.equal(fx.host.ingestRotation(rotation).accepted, true);
  const view = fx.host.view(fx.author);
  assert.deepEqual(view.heads.map((h) => h.digest), [r1.digest]);
  assert.equal(view.commonPrefix.every((e) => e.status === 'certain'), true);
});

test('revocation requires the independent witness threshold', () => {
  const fx = makeFixture(2);
  const chain = new Chain(fx.author, fx.gen0);
  const r0 = chain.next('r0');
  const rotation = makeRotation(fx.author, 0, 1, fx.gen1, r0.digest, fx.gen0);
  const r1 = chain.next('r1', fx.gen1, 1);
  const r2 = chain.next('r2', fx.gen1, 1);
  feedAll(fx.host, [r0]);
  assert.equal(fx.host.ingestRotation(rotation).accepted, true);
  feedAll(fx.host, [r1, r2]);
  const weak = makeRevocation(fx.author, 1, [fx.witnesses[0]]);
  assert.equal(fx.host.ingestRevocation(weak).accepted, false);
  assert.equal(fx.host.view(fx.author).quarantined.length, 0);
  const strong = makeRevocation(fx.author, 1, [fx.witnesses[0], fx.witnesses[1]]);
  assert.equal(fx.host.ingestRevocation(strong).accepted, true);
  const view = fx.host.view(fx.author);
  assert.equal(view.commonPrefix.filter((e) => e.status === 'certain').length, 1);
  assert.deepEqual(
    view.quarantined.map((q) => q.digest).sort(),
    [r1.digest, r2.digest].sort(),
  );
  assert.equal(view.quarantined.every((q) => q.reason === 'revoked-key'), true);
});

test('forged rotation without old-key signature is rejected', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const r0 = chain.next('r0');
  const forged = makeRotation(fx.author, 0, 1, fx.gen1, r0.digest, fx.gen1);
  assert.equal(fx.host.ingestRotation(forged).accepted, false);
});
