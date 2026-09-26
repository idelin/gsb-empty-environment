import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applyMove, applyMovePatch, depthOf, flatten, formatTree,
  insertNode, isDescendant, locate, removeSubtree, validateMove,
} from '../src/tree.ts';
import { makeTree } from '../src/fixtures.ts';

test('applyMove relocates a node and reports the patch', () => {
  const t0 = makeTree();
  const { tree, patch } = applyMove(t0, 'cherry', { parentId: 'veggies', index: 0 });
  assert.deepEqual(patch, {
    kind: 'move', nodeId: 'cherry',
    from: { parentId: 'fruits', index: 2 },
    to: { parentId: 'veggies', index: 0 },
  });
  assert.deepEqual(tree.nodes.veggies.children, ['cherry', 'carrot', 'daikon']);
  assert.deepEqual(tree.nodes.fruits.children, ['apple', 'banana']);
  assert.equal(tree.nodes.cherry.parentId, 'veggies');
  // original tree untouched (immutability)
  assert.deepEqual(t0.nodes.fruits.children, ['apple', 'banana', 'cherry']);
});

test('applyMove within the same parent uses post-removal coordinates', () => {
  const { tree, patch } = applyMove(makeTree(), 'apple', { parentId: 'fruits', index: 2 });
  assert.deepEqual(tree.nodes.fruits.children, ['banana', 'cherry', 'apple']);
  assert.deepEqual(patch.from, { parentId: 'fruits', index: 0 });
  assert.deepEqual(patch.to, { parentId: 'fruits', index: 2 });
});

test('a node can never move into its own descendant', () => {
  const t = makeTree();
  assert.equal(isDescendant(t, 'fruits', 'plantain'), true);
  assert.equal(isDescendant(t, 'fruits', 'carrot'), false);
  assert.match(validateMove(t, 'fruits', { parentId: 'banana', index: 0 })!, /descendant/);
  assert.match(validateMove(t, 'fruits', { parentId: 'plantain', index: 0 })!, /descendant/);
  assert.match(validateMove(t, 'banana', { parentId: 'cavendish', index: 0 })!, /descendant/);
  assert.equal(validateMove(t, 'banana', { parentId: 'veggies', index: 1 }), null);
});

test('validateMove rejects unknown nodes, root moves and bad indices', () => {
  const t = makeTree();
  assert.match(validateMove(t, 'nope', { parentId: 'fruits', index: 0 })!, /unknown node/);
  assert.match(validateMove(t, 'root', { parentId: 'fruits', index: 0 })!, /root/);
  assert.match(validateMove(t, 'apple', { parentId: 'nope', index: 0 })!, /unknown target/);
  assert.match(validateMove(t, 'apple', { parentId: 'fruits', index: 3 })!, /out of range/);
  assert.match(validateMove(t, 'apple', { parentId: 'veggies', index: 3 })!, /out of range/);
});

test('applyMovePatch enforces the recorded source location', () => {
  const t = makeTree();
  const { patch } = applyMove(t, 'apple', { parentId: 'veggies', index: 0 });
  const moved = applyMovePatch(t, patch);
  assert.deepEqual(moved.nodes.veggies.children[0], 'apple');
  assert.throws(
    () => applyMovePatch(moved, patch), // source no longer matches
    /source mismatch/,
  );
});

test('insert/remove/flatten/depth helpers', () => {
  let t = makeTree();
  t = insertNode(t, { id: 'apricot', parentId: null, label: 'Apricot', children: [] }, 'fruits', 1);
  assert.deepEqual(t.nodes.fruits.children, ['apple', 'apricot', 'banana', 'cherry']);
  assert.equal(depthOf(t, 'plantain'), 2);
  t = removeSubtree(t, 'banana');
  assert.equal(t.nodes.banana, undefined);
  assert.equal(t.nodes.plantain, undefined);
  assert.deepEqual(flatten(t).map((r) => r.id),
    ['fruits', 'apple', 'apricot', 'cherry', 'veggies', 'carrot', 'daikon', 'grains', 'rice']);
  assert.equal(locate(t, 'carrot').index, 0);
  assert.match(formatTree(t), /Fruits \[fruits\]/);
});
