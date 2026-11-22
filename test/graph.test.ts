import assert from 'node:assert/strict';
import test from 'node:test';
import { MAX_SEQ, ZERO_DIGEST, utf8 } from '../src/encoding.ts';
import { interpretGraph } from '../src/graph.ts';
import { IngestHost } from '../src/host.ts';
import { generateKey } from '../src/keys.ts';
import { makeRecord, type SignedRecord } from '../src/records.ts';
import { Chain, feedAll, makeFixture, shuffled } from './helpers.ts';

function buildMultiFork(): { all: SignedRecord[]; fx: ReturnType<typeof makeFixture> } {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const base = Array.from({ length: 3 }, (_, i) => chain.next(`base${i}`));
  const a = chain.branch();
  const b = chain.branch();
  const aRecs = [a.next('a3'), a.next('a4'), a.next('a5')];
  const bRecs = [b.next('b3'), b.next('b4')];
  const a1 = a.branch();
  const a2 = a.branch();
  const a1Recs = [a1.next('a6x'), a1.next('a7x')];
  const a2Recs = [a2.next('a6y')];
  return { all: [...base, ...aRecs, ...bRecs, ...a1Recs, ...a2Recs], fx };
}

test('ingest view matches full-record graph interpreter across multiple forks', () => {
  const { all, fx } = buildMultiFork();
  for (const seed of [1, 42, 1337]) {
    const host = new IngestHost(fx.config);
    feedAll(host, shuffled(all, seed));
    const view = host.view(fx.author);
    const oracle = interpretGraph(all);

    assert.deepEqual(view.heads.map((h) => h.digest).sort(), oracle.heads);
    assert.deepEqual(
      view.forks.map((f) => ({ seq: f.seq, digests: f.digests })),
      oracle.forks,
    );
    const tipFromView = view.commonPrefix.filter((e) => e.status === 'certain').at(-1)?.digest ?? null;
    assert.equal(tipFromView, oracle.commonTipDigest);
    for (const rec of all) {
      const expected = oracle.statusByDigest.get(rec.digest);
      if (expected === 'certain') {
        assert.ok(view.readable.some((r) => r.digest === rec.digest), `certain ${rec.digest}`);
      } else {
        assert.ok(
          view.quarantined.some((q) => q.digest === rec.digest),
          `quarantined ${rec.digest}`,
        );
      }
    }
  }
});

test('descendants arriving before ancestors converge to the same view', () => {
  const { all, fx } = buildMultiFork();
  const host = new IngestHost(fx.config);
  const bySeqDesc = [...all].sort((x, y) => (x.data.seq > y.data.seq ? -1 : 1));
  feedAll(host, bySeqDesc);
  const view = host.view(fx.author);
  const oracle = interpretGraph(all);
  assert.deepEqual(view.heads.map((h) => h.digest).sort(), oracle.heads);
  assert.equal(view.quarantined.length, all.length - oracle.prefixDigests.length);
});

test('same key aliased across authors cannot replay records', () => {
  const shared = generateKey();
  const config = {
    authors: [
      { id: 'author-a', gen0Key: shared.publicDer },
      { id: 'author-b', gen0Key: shared.publicDer },
    ],
  };
  const host = new IngestHost(config);
  const chainA = new Chain('author-a', shared);
  const r0 = chainA.next('hello');
  assert.equal(host.ingestRecord(r0).accepted, true);

  const replayed = makeRecord(shared, {
    author: 'author-b',
    keyGen: 0,
    seq: 0n,
    prev: ZERO_DIGEST,
    body: utf8('hello'),
  });
  replayed.signature = r0.signature;
  replayed.signerKey = r0.signerKey;
  replayed.digest = r0.digest;
  const res = host.ingestRecord(replayed);
  assert.equal(res.accepted, false);
  assert.equal(res.reason, 'digest-mismatch');
  assert.equal(host.view('author-b').heads.length, 0);
});

test('restoring an old snapshot never regresses a newer state', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0);
  const records = Array.from({ length: 10 }, (_, i) => chain.next(`m${i}`));
  feedAll(fx.host, records.slice(0, 5));
  const oldSnapshot = fx.host.snapshot();
  feedAll(fx.host, records.slice(5));
  const before = fx.host.view(fx.author);

  fx.host.mergeSnapshot(oldSnapshot);
  const after = fx.host.view(fx.author);
  assert.deepEqual(after.heads, before.heads);
  assert.equal(after.readable.length, 10);

  const restored = IngestHost.restore(oldSnapshot, fx.config);
  assert.equal(restored.view(fx.author).readable.length, 5);
  feedAll(restored, records.slice(5));
  const restoredView = restored.view(fx.author);
  assert.deepEqual(
    restoredView.heads.map((h) => h.digest),
    before.heads.map((h) => h.digest),
  );
  assert.equal(restoredView.readable.length, 10);

  const roundTrip = IngestHost.restore(fx.host.snapshot(), fx.config);
  assert.equal(roundTrip.view(fx.author).readable.length, 10);
});

test('64-bit sequence boundary is honored', () => {
  const fx = makeFixture();
  const chain = new Chain(fx.author, fx.gen0, MAX_SEQ - 1n, ZERO_DIGEST);
  const r0 = chain.next('near-max');
  const r1 = chain.next('at-max');
  feedAll(fx.host, [r0, r1]);
  const view = fx.host.view(fx.author);
  assert.equal(view.heads.length, 1);
  assert.equal(view.heads[0].seq, MAX_SEQ);
  assert.equal(view.commonPrefix.filter((e) => e.status === 'certain').length, 2);

  assert.throws(() => chain.next('overflow'), /u64 out of range/);
});
