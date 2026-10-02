import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MerkleTree } from '../src/merkle.ts';
import { ShardTree, encodeAccount } from '../src/shard.ts';

function makeShard(): ShardTree {
  return new ShardTree(2, [
    { id: 'carol', balance: 1000 },
    { id: 'dave', balance: 300 },
  ]);
}

test('transfer moves balances and changes the local root', () => {
  const shard = makeShard();
  const before = shard.getLocalRoot();
  shard.applyTransfer('carol', 'dave', 250);
  assert.equal(shard.getAccount('carol')!.balance, 750);
  assert.equal(shard.getAccount('dave')!.balance, 550);
  assert.notEqual(shard.getLocalRoot(), before);
});

test('transfer rejects unknown accounts and insufficient funds', () => {
  const shard = makeShard();
  assert.throws(() => shard.applyTransfer('ghost', 'dave', 1), /unknown sender/);
  assert.throws(() => shard.applyTransfer('carol', 'dave', 10_000), /insufficient funds/);
});

test('account inclusion proof verifies against the local root', () => {
  const shard = makeShard();
  shard.applyTransfer('carol', 'dave', 250);
  const proof = shard.getAccountProof('dave');
  assert.equal(proof.account.balance, 550);
  assert.ok(
    MerkleTree.verifyProof(
      encodeAccount(proof.account),
      proof.proof,
      Buffer.from(proof.shardRoot, 'hex'),
    ),
  );
});

test('shard grows beyond initial capacity while proofs stay valid', () => {
  const shard = new ShardTree(0);
  for (let i = 0; i < 20; i++) shard.upsertAccount({ id: `user-${i}`, balance: i * 10 });
  for (let i = 0; i < 20; i++) {
    const proof = shard.getAccountProof(`user-${i}`);
    assert.ok(
      MerkleTree.verifyProof(
        encodeAccount(proof.account),
        proof.proof,
        Buffer.from(proof.shardRoot, 'hex'),
      ),
    );
  }
});
