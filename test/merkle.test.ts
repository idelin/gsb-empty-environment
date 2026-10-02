import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MerkleTree, hashLeaf, leafIndexFromProof } from '../src/merkle.ts';

test('inclusion proofs verify for every leaf', () => {
  const tree = new MerkleTree(4);
  const data = ['a', 'b', 'c', 'd'].map((s) => Buffer.from(s));
  data.forEach((d, i) => tree.setLeaf(i, d));
  const root = tree.getRoot();
  data.forEach((d, i) => {
    assert.ok(MerkleTree.verifyProof(d, tree.getProof(i), root), `leaf ${i}`);
  });
});

test('proof path encodes the leaf index', () => {
  const tree = new MerkleTree(4);
  for (let i = 0; i < 4; i++) tree.setLeaf(i, Buffer.from(`leaf-${i}`));
  for (let i = 0; i < 4; i++) {
    assert.equal(leafIndexFromProof(tree.getProof(i)), i);
  }
});

test('verification fails for wrong leaf data or wrong root', () => {
  const tree = new MerkleTree(4);
  tree.setLeaf(0, Buffer.from('real'));
  const proof = tree.getProof(0);
  const root = tree.getRoot();
  assert.ok(!MerkleTree.verifyProof(Buffer.from('fake'), proof, root));
  assert.ok(!MerkleTree.verifyProof(Buffer.from('real'), proof, hashLeaf(Buffer.from('other'))));
});

test('leaf updates change the root, and new proofs verify', () => {
  const tree = new MerkleTree(4);
  tree.setLeaf(1, Buffer.from('v1'));
  const root1 = tree.getRoot();
  tree.setLeaf(1, Buffer.from('v2'));
  const root2 = tree.getRoot();
  assert.notDeepEqual(root1, root2);
  assert.ok(MerkleTree.verifyProof(Buffer.from('v2'), tree.getProof(1), root2));
  assert.ok(!MerkleTree.verifyProof(Buffer.from('v1'), tree.getProof(1), root2));
});

test('tree grows dynamically beyond initial capacity', () => {
  const tree = new MerkleTree(2);
  assert.equal(tree.capacity, 2);
  for (let i = 0; i < 9; i++) tree.setLeaf(i, Buffer.from(`item-${i}`));
  assert.ok(tree.capacity >= 16);
  const root = tree.getRoot();
  for (let i = 0; i < 9; i++) {
    assert.ok(MerkleTree.verifyProof(Buffer.from(`item-${i}`), tree.getProof(i), root));
  }
});
