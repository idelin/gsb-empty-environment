import { test } from 'node:test';
import assert from 'node:assert/strict';
import { chunk } from '../src/chunker.ts';
import { buildMerkleTree, getProof, verifyProof } from '../src/merkle.ts';
import { randomBytes } from '../src/prng.ts';

test('every leaf proof verifies against the root', () => {
  const data = randomBytes(1_000_000, 20);
  const leaves = chunk(data).map((c) => c.hash);
  const tree = buildMerkleTree(leaves);
  for (let i = 0; i < leaves.length; i++) {
    assert.ok(verifyProof(leaves[i], getProof(tree, i), tree.root), `leaf ${i}`);
  }
});

test('tampering with a leaf changes the root and fails verification', () => {
  const data = randomBytes(500_000, 21);
  const leaves = chunk(data).map((c) => c.hash);
  const tree = buildMerkleTree(leaves);
  const tampered = leaves.slice();
  tampered[3] = '00'.repeat(32);
  const tamperedTree = buildMerkleTree(tampered);
  assert.notEqual(tamperedTree.root, tree.root);
  assert.ok(!verifyProof(tampered[3], getProof(tree, 3), tree.root));
});

test('handles odd leaf counts (promotion)', () => {
  const leaves = Array.from({ length: 7 }, (_, i) =>
    randomBytes(32, 100 + i).toString('hex'),
  );
  const tree = buildMerkleTree(leaves);
  assert.equal(tree.levels[0].length, 7);
  for (let i = 0; i < leaves.length; i++) {
    assert.ok(verifyProof(leaves[i], getProof(tree, i), tree.root));
  }
});

test('single-leaf tree root equals the leaf', () => {
  const leaf = randomBytes(32, 42).toString('hex');
  const tree = buildMerkleTree([leaf]);
  assert.equal(tree.root, leaf);
  assert.deepEqual(getProof(tree, 0), []);
});
