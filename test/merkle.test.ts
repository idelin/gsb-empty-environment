import test from 'node:test';
import assert from 'node:assert/strict';
import { BUCKET_COUNT, BRANCHING_FACTOR, diffByDepth, MerkleTree, type TreeAccessor } from '../src/merkle.ts';
import type { NodeState } from '../src/types.ts';

const state = (nodeId: string, status: 'alive' | 'dead' = 'alive', version = 1): NodeState => ({
  nodeId,
  status,
  version,
  updatedBy: nodeId,
});

const acc = (tree: MerkleTree): TreeAccessor => ({
  rootHash: () => tree.rootHash,
  childrenOf: (path: number[]) => tree.childrenOf(path),
});

const FULL_TREE_COMPARISONS = BUCKET_COUNT + BUCKET_COUNT / BRANCHING_FACTOR;

test('identical datasets produce identical root hashes', () => {
  const a = new MerkleTree([state('n0'), state('n1'), state('n2')]);
  const b = new MerkleTree([state('n2'), state('n1'), state('n0')]);
  assert.equal(a.rootHash, b.rootHash);
});

test('diffByDepth returns no keys for identical trees', () => {
  const states = [state('n0'), state('n1')];
  const diff = ((t) => diffByDepth(acc(t), acc(t)))(new MerkleTree(states));
  assert.deepEqual(diff.keys, []);
  assert.equal(diff.nodesCompared, 0);
  assert.equal(diff.roundTrips, 0);
});

test('a single divergent key is located exactly, with tree pruning', () => {
  const base = Array.from({ length: 8 }, (_, i) => state(`n${i}`));
  const changed = base.map((s) => (s.nodeId === 'n5' ? state('n5', 'dead', 2) : s));
  const diff = diffByDepth(acc(new MerkleTree(base)), acc(new MerkleTree(changed)));
  assert.deepEqual(diff.keys, ['n5']);
  assert.equal(diff.levelsTraversed, 2);
  assert.ok(
    diff.nodesCompared < FULL_TREE_COMPARISONS,
    `expected pruning (${diff.nodesCompared} < ${FULL_TREE_COMPARISONS})`,
  );
});

test('a key missing on one side is located exactly', () => {
  const local = [state('n0'), state('n1'), state('n2')];
  const remote = [state('n0'), state('n1')];
  const diff = diffByDepth(acc(new MerkleTree(local)), acc(new MerkleTree(remote)));
  assert.deepEqual(diff.keys, ['n2']);
});

test('empty tree vs populated tree yields every key', () => {
  const remote = [state('n0'), state('n1'), state('n2'), state('n3')];
  const diff = diffByDepth(acc(new MerkleTree([])), acc(new MerkleTree(remote)));
  assert.deepEqual(diff.keys, ['n0', 'n1', 'n2', 'n3']);
});
