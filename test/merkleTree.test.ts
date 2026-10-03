import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MerkleTree, verifyProof } from '../src/merkleTree.ts';

test('empty tree has a deterministic root', () => {
  const a = new MerkleTree();
  const b = new MerkleTree();
  assert.ok(a.root().equals(b.root()));
});

test('append assigns sequential indices and changes the root', () => {
  const tree = new MerkleTree();
  const roots: string[] = [];
  for (let i = 0; i < 5; i++) {
    assert.equal(tree.append(Buffer.from(`leaf-${i}`)), i);
    roots.push(tree.root().toString('hex'));
  }
  assert.equal(new Set(roots).size, 5, 'every append must change the root');
});

test('inclusion proofs verify for every leaf (odd and even sizes)', () => {
  for (const n of [1, 2, 3, 4, 5, 7, 8, 13]) {
    const tree = new MerkleTree();
    const leaves = Array.from({ length: n }, (_, i) => Buffer.from(`item-${i}`));
    for (const leaf of leaves) tree.append(leaf);
    const root = tree.root();
    for (let i = 0; i < n; i++) {
      assert.ok(
        verifyProof(leaves[i], tree.getProof(i), root),
        `proof for leaf ${i} of ${n} must verify`,
      );
    }
  }
});

test('update changes the root and old proofs become invalid', () => {
  const tree = new MerkleTree();
  for (let i = 0; i < 6; i++) tree.append(Buffer.from(`v0-${i}`));
  const rootBefore = tree.root().toString('hex');
  const staleProof = tree.getProof(3);

  tree.update(3, Buffer.from('v1-3'));
  const rootAfter = tree.root().toString('hex');
  assert.notEqual(rootBefore, rootAfter);

  assert.ok(verifyProof(Buffer.from('v1-3'), tree.getProof(3), tree.root()));
  assert.ok(!verifyProof(Buffer.from('v0-3'), staleProof, tree.root()));
});

test('tampered proofs are rejected', () => {
  const tree = new MerkleTree();
  for (let i = 0; i < 4; i++) tree.append(Buffer.from(`x-${i}`));
  const proof = tree.getProof(1);
  const root = tree.root();

  const wrongLeaf = { ...proof };
  assert.ok(!verifyProof(Buffer.from('forged'), wrongLeaf, root));

  const flipped = structuredClone(proof);
  flipped.path[0].position = flipped.path[0].position === 'left' ? 'right' : 'left';
  assert.ok(!verifyProof(Buffer.from('x-1'), flipped, root));

  const otherTree = new MerkleTree();
  otherTree.append(Buffer.from('x-1'));
  assert.ok(!verifyProof(Buffer.from('x-1'), proof, otherTree.root()));
});

test('out-of-range access throws', () => {
  const tree = new MerkleTree();
  tree.append(Buffer.from('only'));
  assert.throws(() => tree.getProof(1), RangeError);
  assert.throws(() => tree.update(-1, Buffer.from('nope')), RangeError);
  assert.throws(() => tree.leafHashAt(5), RangeError);
});
