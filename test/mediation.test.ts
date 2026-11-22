import assert from 'node:assert/strict';
import test from 'node:test';
import { makeMediation } from '../src/certs.ts';
import { Chain, feedAll, makeFixture, shuffled } from './helpers.ts';

function forkedHost() {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const base = Array.from({ length: 5 }, (_, i) => chain.next(`base${i}`));
  const branchA = chain.branch();
  const branchB = chain.branch();
  const aRecs = [branchA.next('a5'), branchA.next('a6'), branchA.next('a7')];
  const bRecs = [branchB.next('b5'), branchB.next('b6')];
  feedAll(fx.host, shuffled([...base, ...aRecs, ...bRecs], 9));
  return { fx, base, aRecs, bRecs, branchB };
}

test('mediation cert naming all known heads resolves the fork', () => {
  const { fx, base, aRecs, bRecs } = forkedHost();
  const heads = [aRecs[2].digest, bRecs[1].digest];
  const cert = makeMediation(fx.mediator, fx.author, aRecs[2].digest, heads, 7n);
  assert.equal(fx.host.ingestMediation(cert).accepted, true);
  const view = fx.host.view(fx.author);
  assert.equal(view.mediation.applied, true);
  assert.equal(view.mediation.chooses, aRecs[2].digest);
  assert.deepEqual(
    view.readable.map((r) => r.digest),
    [...base, ...aRecs].map((r) => r.digest),
  );
  assert.deepEqual(
    view.quarantined.map((q) => q.digest).sort(),
    bRecs.map((r) => r.digest).sort(),
  );
});

test('cert that omits a known conflicting head is not applied', () => {
  const { fx, aRecs } = forkedHost();
  const cert = makeMediation(fx.mediator, fx.author, aRecs[2].digest, [aRecs[2].digest], 7n);
  assert.equal(fx.host.ingestMediation(cert).accepted, true);
  const view = fx.host.view(fx.author);
  assert.equal(view.mediation.applied, false);
  assert.ok(view.mediation.staleReason?.startsWith('unnamed-head:'));
  assert.equal(view.quarantined.length, 5);
});

test('late-discovered head inside the observed scope voids the old decision', () => {
  const { fx, aRecs, bRecs, branchB } = forkedHost();
  const heads = [aRecs[2].digest, bRecs[1].digest];
  const cert = makeMediation(fx.mediator, fx.author, aRecs[2].digest, heads, 7n);
  assert.equal(fx.host.ingestMediation(cert).accepted, true);
  assert.equal(fx.host.view(fx.author).mediation.applied, true);

  const b7 = branchB.next('b7');
  feedAll(fx.host, [b7]);
  const view = fx.host.view(fx.author);
  assert.equal(view.mediation.applied, false);
  assert.equal(view.mediation.staleReason, `unnamed-head:${b7.digest}`);
  assert.equal(view.quarantined.length, 6);
  assert.equal(view.lifted.length, 0);
});

test('head beyond the observed scope does not void the decision', () => {
  const { fx, aRecs, bRecs, branchB } = forkedHost();
  const heads = [aRecs[2].digest, bRecs[1].digest];
  const cert = makeMediation(fx.mediator, fx.author, aRecs[2].digest, heads, 7n);
  assert.equal(fx.host.ingestMediation(cert).accepted, true);
  const b7 = branchB.next('b7');
  const b8 = branchB.next('b8');
  feedAll(fx.host, [b7, b8]);
  const wider = makeMediation(
    fx.mediator,
    fx.author,
    aRecs[2].digest,
    [aRecs[2].digest, b8.digest],
    8n,
  );
  assert.equal(fx.host.ingestMediation(wider).accepted, true);
  const view = fx.host.view(fx.author);
  assert.equal(view.mediation.applied, true);
  assert.equal(view.mediation.chooses, aRecs[2].digest);
});

test('cert from an untrusted mediator is rejected', () => {
  const { fx, aRecs, bRecs } = forkedHost();
  const stranger = makeFixture().mediator;
  const cert = makeMediation(stranger, fx.author, aRecs[2].digest, [aRecs[2].digest, bRecs[1].digest], 7n);
  assert.equal(fx.host.ingestMediation(cert).accepted, false);
});
