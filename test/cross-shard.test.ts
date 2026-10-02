import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Aggregator, verifyCrossShardProof } from '../src/aggregator.ts';
import type { CrossShardProof } from '../src/aggregator.ts';

function bootSystem(): Aggregator {
  const aggregator = new Aggregator(4);
  aggregator.getShard(0).upsertAccount({ id: 'alice', balance: 500 });
  aggregator.getShard(1).upsertAccount({ id: 'bob', balance: 800 });
  aggregator.getShard(2).upsertAccount({ id: 'carol', balance: 1000 });
  aggregator.getShard(2).upsertAccount({ id: 'dave', balance: 300 });
  aggregator.getShard(3).upsertAccount({ id: 'erin', balance: 150 });
  aggregator.syncAllShards();
  return aggregator;
}

function transferInShard2(aggregator: Aggregator): string {
  aggregator.getShard(2).applyTransfer('carol', 'dave', 250);
  aggregator.syncShard(2);
  return aggregator.getMetaRoot();
}

test('acceptance: transfer in shard 2 yields an independently verifiable combined proof', () => {
  const aggregator = bootSystem();
  const trustedMetaRoot = transferInShard2(aggregator);

  const proof = aggregator.buildCrossShardProof(2, 'dave');
  assert.equal(proof.account.balance, 550);
  assert.equal(proof.shardId, 2);
  assert.ok(proof.shardProof.length > 0, 'shard-internal inclusion proof attached');
  assert.ok(proof.metaProof.length > 0, 'meta-tree position proof attached');

  // External verifier: only the JSON wire format and the trusted meta root.
  const received: CrossShardProof = JSON.parse(JSON.stringify(proof));
  assert.ok(verifyCrossShardProof(received, trustedMetaRoot));
});

test('combined proof is rejected against a stale meta root', () => {
  const aggregator = bootSystem();
  const staleRoot = aggregator.getMetaRoot();
  transferInShard2(aggregator);
  const proof = aggregator.buildCrossShardProof(2, 'dave');
  assert.ok(!verifyCrossShardProof(proof, staleRoot));
});

test('tampered account balance fails verification', () => {
  const aggregator = bootSystem();
  const trustedMetaRoot = transferInShard2(aggregator);
  const proof = aggregator.buildCrossShardProof(2, 'dave');
  const forged: CrossShardProof = { ...proof, account: { id: 'dave', balance: 999_999 } };
  assert.ok(!verifyCrossShardProof(forged, trustedMetaRoot));
});

test('relabeling the shard id fails verification', () => {
  const aggregator = bootSystem();
  const trustedMetaRoot = transferInShard2(aggregator);
  const proof = aggregator.buildCrossShardProof(2, 'dave');
  const relabeled: CrossShardProof = { ...proof, shardId: 1 };
  assert.ok(!verifyCrossShardProof(relabeled, trustedMetaRoot));
});

test('shards update roots independently of each other', () => {
  const aggregator = bootSystem();
  const root0Before = aggregator.getShard(0).getLocalRoot();
  const root3Before = aggregator.getShard(3).getLocalRoot();
  transferInShard2(aggregator);
  assert.equal(aggregator.getShard(0).getLocalRoot(), root0Before);
  assert.equal(aggregator.getShard(3).getLocalRoot(), root3Before);
});
