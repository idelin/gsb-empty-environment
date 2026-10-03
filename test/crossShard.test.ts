import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Aggregator, SHARD_COUNT, verifyCrossShardProof } from '../src/index.ts';
import type { CrossShardProof } from '../src/index.ts';

function buildSystem(): Aggregator {
  const agg = new Aggregator();
  agg.shard(0).setAccount('carol', { balance: 500n, nonce: 0 });
  agg.shard(1).setAccount('dave', { balance: 300n, nonce: 0 });
  agg.shard(2).setAccount('alice', { balance: 1000n, nonce: 0 });
  agg.shard(2).setAccount('bob', { balance: 250n, nonce: 0 });
  agg.shard(3).setAccount('erin', { balance: 800n, nonce: 0 });
  return agg;
}

test('system has 4 independent shards with independent roots', () => {
  const agg = buildSystem();
  assert.equal(agg.shards.length, SHARD_COUNT);
  const roots = agg.shards.map((s) => s.root().toString('hex'));
  assert.equal(new Set(roots).size, SHARD_COUNT, 'shard roots must be independent');

  const metaBefore = agg.metaRoot().toString('hex');
  agg.shard(1).setAccount('dave', { balance: 301n, nonce: 1 });
  const metaAfter = agg.metaRoot().toString('hex');
  assert.notEqual(metaBefore, metaAfter, 'meta root must track shard root changes');
});

test('acceptance: shard-2 transfer yields an externally verifiable combined proof', () => {
  const agg = buildSystem();

  // Account in shard 2 initiates a transfer.
  agg.shard(2).applyTransfer('alice', 'bob', 400n);

  // Shard 2 internal inclusion proof + Meta-Tree position proof.
  const proof = agg.buildCrossShardProof(2, 'alice');
  const metaRoot = agg.metaRoot().toString('hex');

  // Combined proof is self-contained and JSON-serializable.
  const roundTripped: CrossShardProof = JSON.parse(JSON.stringify(proof));

  // Arbitrary external verifier checks it independently.
  assert.ok(verifyCrossShardProof(roundTripped));
  assert.ok(verifyCrossShardProof(roundTripped, metaRoot));

  // Proof content sanity checks.
  assert.equal(proof.shardId, 2);
  assert.equal(proof.accountState.balance, '600');
  assert.equal(proof.accountState.nonce, 1);
  assert.equal(proof.metaProof.leafIndex, 2, 'shard 2 sits at index 2 in the Meta-Tree');
});

test('proofs exist for accounts in every shard', () => {
  const agg = buildSystem();
  const metaRoot = agg.metaRoot().toString('hex');
  for (const [shardId, account] of [
    [0, 'carol'],
    [1, 'dave'],
    [2, 'alice'],
    [3, 'erin'],
  ] as const) {
    const proof = agg.buildCrossShardProof(shardId, account);
    assert.ok(verifyCrossShardProof(proof, metaRoot), `shard ${shardId} proof must verify`);
  }
});

test('external verifier rejects tampered or mismatched proofs', () => {
  const agg = buildSystem();
  agg.shard(2).applyTransfer('alice', 'bob', 400n);
  const proof = agg.buildCrossShardProof(2, 'alice');
  const metaRoot = agg.metaRoot().toString('hex');

  // Tampered balance.
  const badBalance = structuredClone(proof);
  badBalance.accountState.balance = '999999';
  assert.ok(!verifyCrossShardProof(badBalance, metaRoot));

  // Wrong shard id (leaf encoding changes).
  const badShard = structuredClone(proof);
  badShard.shardId = 1;
  assert.ok(!verifyCrossShardProof(badShard, metaRoot));

  // Wrong trusted meta root.
  assert.ok(!verifyCrossShardProof(proof, '00'.repeat(32)));

  // Stale proof after further state changes.
  agg.shard(2).applyTransfer('alice', 'bob', 100n);
  assert.ok(!verifyCrossShardProof(proof, agg.metaRoot().toString('hex')));

  // Swapped meta proof from another shard.
  const other = agg.buildCrossShardProof(0, 'carol');
  const swapped = structuredClone(proof);
  swapped.metaProof = other.metaProof;
  assert.ok(!verifyCrossShardProof(swapped));
});

test('combined proof remains valid across unrelated shard updates', () => {
  const agg = buildSystem();
  const proof = agg.buildCrossShardProof(2, 'alice');

  // Unrelated shard updates change the meta root, so the old proof must not
  // verify against the NEW meta root, but still verifies against its own.
  agg.shard(3).setAccount('erin', { balance: 801n, nonce: 1 });
  assert.ok(verifyCrossShardProof(proof));
  assert.ok(!verifyCrossShardProof(proof, agg.metaRoot().toString('hex')));
});
