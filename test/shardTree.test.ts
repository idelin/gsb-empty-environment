import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ShardTree } from '../src/shardTree.ts';
import { verifyProof } from '../src/merkleTree.ts';
import { encodeAccountLeaf } from '../src/shardTree.ts';

test('accounts can be added and updated dynamically, root tracks state', () => {
  const shard = new ShardTree(2);
  shard.setAccount('alice', { balance: 1000n, nonce: 0 });
  const r1 = shard.root().toString('hex');
  shard.setAccount('bob', { balance: 250n, nonce: 0 });
  const r2 = shard.root().toString('hex');
  shard.setAccount('alice', { balance: 900n, nonce: 1 });
  const r3 = shard.root().toString('hex');
  assert.notEqual(r1, r2);
  assert.notEqual(r2, r3);
  assert.equal(shard.size, 2);
  assert.deepEqual(shard.getAccount('alice'), { balance: 900n, nonce: 1 });
});

test('shard inclusion proof verifies against the shard local root', () => {
  const shard = new ShardTree(2);
  shard.setAccount('alice', { balance: 1000n, nonce: 0 });
  shard.setAccount('bob', { balance: 250n, nonce: 0 });
  shard.setAccount('carol', { balance: 75n, nonce: 3 });

  const inclusion = shard.getInclusionProof('bob');
  const leaf = encodeAccountLeaf(2, 'bob', { balance: 250n, nonce: 0 });
  assert.ok(verifyProof(leaf, inclusion.proof, Buffer.from(inclusion.shardRoot, 'hex')));
});

test('intra-shard transfer debits sender, credits receiver, bumps nonce', () => {
  const shard = new ShardTree(2);
  shard.setAccount('alice', { balance: 1000n, nonce: 0 });
  shard.setAccount('bob', { balance: 250n, nonce: 0 });
  const rootBefore = shard.root().toString('hex');

  shard.applyTransfer('alice', 'bob', 400n);

  assert.deepEqual(shard.getAccount('alice'), { balance: 600n, nonce: 1 });
  assert.deepEqual(shard.getAccount('bob'), { balance: 650n, nonce: 0 });
  assert.notEqual(shard.root().toString('hex'), rootBefore);
});

test('transfer validation: insufficient funds and unknown accounts', () => {
  const shard = new ShardTree(2);
  shard.setAccount('alice', { balance: 10n, nonce: 0 });
  shard.setAccount('bob', { balance: 0n, nonce: 0 });
  assert.throws(() => shard.applyTransfer('alice', 'bob', 11n), /insufficient/);
  assert.throws(() => shard.applyTransfer('alice', 'nobody', 1n), /unknown receiver/);
  assert.throws(() => shard.applyTransfer('nobody', 'bob', 1n), /unknown sender/);
  assert.throws(() => shard.applyTransfer('alice', 'bob', 0n), /positive/);
});

test('proof for unknown account throws', () => {
  const shard = new ShardTree(0);
  assert.throws(() => shard.getInclusionProof('ghost'), /unknown account/);
});
