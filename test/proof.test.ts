import assert from 'node:assert/strict';
import test from 'node:test';
import { makeCheckpoint, makeMediation } from '../src/certs.ts';
import { buildProof, compressProof, expandRanges } from '../src/proof.ts';
import type { SignedRecord } from '../src/records.ts';
import { Chain, feedAll, makeFixture, shuffled } from './helpers.ts';

test('proof marks certain, quarantined and unknown for checkpoint-only readers', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const records = Array.from({ length: 6 }, (_, i) => chain.next(`m${i}`));
  const branchA = chain.branch();
  const branchB = chain.branch();
  const aRecs = [branchA.next('a6'), branchA.next('a7')];
  const bRecs = [branchB.next('b6'), branchB.next('b7')];

  const cp = makeCheckpoint(fx.relay, fx.author, 3n, records[3].digest);
  assert.equal(fx.host.ingestCheckpoint(cp).accepted, true);
  feedAll(fx.host, shuffled([...records.slice(4), ...aRecs, ...bRecs], 13));

  const proof = buildProof(fx.host.view(fx.author));
  const at = (seq: bigint) => proof.entries.filter((e) => e.seq === seq);
  assert.equal(proof.unknownBelow, 3n);
  assert.ok(at(3n).every((e) => e.status === 'unknown' && e.digest === records[3].digest));
  assert.ok(at(5n).every((e) => e.status === 'certain'));
  assert.equal(at(6n).length, 2);
  assert.ok(at(6n).every((e) => e.status === 'quarantined'));
  assert.ok(at(7n).every((e) => e.status === 'quarantined'));

  const readerCheckpoint = proof.entries.find((e) => e.seq === 3n);
  assert.equal(readerCheckpoint?.digest, records[3].digest);
});

test('fork-free certain prefix compresses losslessly into one range', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const records = Array.from({ length: 12 }, (_, i) => chain.next(`m${i}`));
  feedAll(fx.host, shuffled(records, 17));

  const proof = buildProof(fx.host.view(fx.author));
  const ranges = compressProof(proof);
  assert.equal(ranges.length, 1);
  assert.equal(ranges[0].from, 0n);
  assert.equal(ranges[0].to, 11n);
  assert.equal(ranges[0].status, 'certain');
  assert.equal(ranges[0].tipDigest, records[11].digest);

  const byDigest = new Map<string, SignedRecord>(records.map((r) => [r.digest, r]));
  const expanded = expandRanges(ranges, byDigest);
  assert.deepEqual(expanded, proof.entries);
});

test('compression keeps forked seqs explicit and round-trips', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const base = Array.from({ length: 4 }, (_, i) => chain.next(`m${i}`));
  const branchA = chain.branch();
  const branchB = chain.branch();
  const aRecs = [branchA.next('a4'), branchA.next('a5')];
  const bRecs = [branchB.next('b4'), branchB.next('b5')];
  feedAll(fx.host, shuffled([...base, ...aRecs, ...bRecs], 19));

  const proof = buildProof(fx.host.view(fx.author));
  const ranges = compressProof(proof);
  const certainRange = ranges.find((r) => r.status === 'certain');
  assert.equal(certainRange?.from, 0n);
  assert.equal(certainRange?.to, 3n);
  const forkRanges = ranges.filter((r) => r.status === 'quarantined');
  assert.equal(forkRanges.length, 4);
  assert.ok(forkRanges.every((r) => r.from === r.to));

  const all = [...base, ...aRecs, ...bRecs];
  const byDigest = new Map<string, SignedRecord>(all.map((r) => [r.digest, r]));
  assert.deepEqual(expandRanges(ranges, byDigest), proof.entries);
});

test('mediation-resolved records appear certain in the proof', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const base = Array.from({ length: 3 }, (_, i) => chain.next(`m${i}`));
  const branchA = chain.branch();
  const branchB = chain.branch();
  const aRecs = [branchA.next('a3')];
  const bRecs = [branchB.next('b3')];
  feedAll(fx.host, [...base, ...aRecs, ...bRecs]);
  const cert = makeMediation(fx.mediator, fx.author, aRecs[0].digest, [aRecs[0].digest, bRecs[0].digest], 3n);
  assert.equal(fx.host.ingestMediation(cert).accepted, true);

  const proof = buildProof(fx.host.view(fx.author));
  const at3 = proof.entries.filter((e) => e.seq === 3n);
  assert.equal(at3.find((e) => e.digest === aRecs[0].digest)?.status, 'certain');
  assert.equal(at3.find((e) => e.digest === bRecs[0].digest)?.status, 'quarantined');
});
