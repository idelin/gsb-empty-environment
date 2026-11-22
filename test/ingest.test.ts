import assert from 'node:assert/strict';
import test from 'node:test';
import { makeCheckpoint } from '../src/certs.ts';
import { Chain, feedAll, makeFixture, shuffled } from './helpers.ts';

test('out-of-order deltas with relay checkpoints keep gaps explicit', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const records = Array.from({ length: 10 }, (_, i) => chain.next(`m${i}`));

  const cp = makeCheckpoint(fx.relay, fx.author, 4n, records[4].digest);
  assert.equal(fx.host.ingestCheckpoint(cp).accepted, true);
  feedAll(fx.host, shuffled(records.slice(5), 7));

  let view = fx.host.view(fx.author);
  assert.deepEqual(view.heads.map((h) => h.digest), [records[9].digest]);
  assert.equal(view.unknownPrefixBelow, 4n);
  assert.equal(view.commonPrefix.length, 6);
  assert.equal(view.commonPrefix[0].seq, 4n);
  assert.equal(view.commonPrefix[0].digest, records[4].digest);
  assert.equal(view.commonPrefix[0].hasContent, false);
  assert.equal(view.commonPrefix[0].status, 'unknown');
  for (const e of view.commonPrefix.slice(1)) {
    assert.equal(e.status, 'certain');
    assert.equal(e.hasContent, true);
  }
  assert.deepEqual(
    view.gaps.map((g) => g.kind).sort(),
    ['missing-content'],
  );
  assert.equal(view.readable.length, 5);

  feedAll(fx.host, shuffled(records.slice(0, 5), 11));
  view = fx.host.view(fx.author);
  assert.equal(view.unknownPrefixBelow, 0n);
  assert.equal(view.commonPrefix.every((e) => e.status === 'certain'), true);
  assert.equal(view.gaps.length, 0);
  assert.equal(view.readable.length, 10);
});

test('same-seq equivocation quarantines descendants but prefix stays readable', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const base = Array.from({ length: 5 }, (_, i) => chain.next(`base${i}`));
  const branchA = chain.branch();
  const branchB = chain.branch();
  const aRecs = [branchA.next('a5'), branchA.next('a6'), branchA.next('a7')];
  const bRecs = [branchB.next('b5'), branchB.next('b6'), branchB.next('b7')];

  feedAll(fx.host, shuffled([...base, ...aRecs, ...bRecs], 3));
  const view = fx.host.view(fx.author);

  assert.equal(view.forks.length, 1);
  assert.equal(view.forks[0].seq, 5n);
  assert.deepEqual(view.forks[0].digests, [aRecs[0].digest, bRecs[0].digest].sort());

  assert.equal(view.commonPrefix.length, 5);
  assert.equal(view.commonPrefix.every((e) => e.status === 'certain'), true);
  assert.deepEqual(view.readable.map((r) => r.digest), base.map((r) => r.digest));

  assert.equal(view.quarantined.length, 6);
  assert.equal(view.quarantined.every((q) => q.reason === 'fork-conflict'), true);
  assert.deepEqual(
    view.heads.map((h) => h.digest).sort(),
    [aRecs[2].digest, bRecs[2].digest].sort(),
  );
});

test('conflicting relay checkpoint against held record surfaces as fork', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const records = Array.from({ length: 4 }, (_, i) => chain.next(`m${i}`));
  feedAll(fx.host, records);

  const evil = new Chain(fx.author, fx.gen0);
  for (let i = 0; i < 2; i++) evil.next(`m${i}`);
  const alien = evil.next('forged');
  const cp = makeCheckpoint(fx.relay, fx.author, 2n, alien.digest);
  assert.equal(fx.host.ingestCheckpoint(cp).accepted, true);

  const view = fx.host.view(fx.author);
  assert.equal(view.forks.length, 1);
  assert.equal(view.forks[0].seq, 2n);
  assert.equal(view.commonPrefix.filter((e) => e.status === 'certain').length, 0);
  const alienHead = view.heads.find((h) => h.digest === alien.digest);
  assert.equal(alienHead?.hasContent, false);
  assert.equal(view.gaps.some((g) => g.kind === 'missing-content' && g.digest === alien.digest), true);
});
