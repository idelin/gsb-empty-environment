import assert from 'node:assert/strict';
import { test } from 'node:test';
import { RedBlackTree } from '../src/redBlackTree.ts';

test('ceiling returns the smallest key >= query', () => {
  const tree = new RedBlackTree<string>();
  tree.insert(10, 'a');
  tree.insert(20, 'b');
  tree.insert(30, 'c');
  assert.equal(tree.ceiling(5)?.value, 'a');
  assert.equal(tree.ceiling(10)?.value, 'a');
  assert.equal(tree.ceiling(11)?.value, 'b');
  assert.equal(tree.ceiling(30)?.value, 'c');
  assert.equal(tree.ceiling(31), null);
  assert.equal(tree.minimum()?.value, 'a');
  assert.equal(tree.size, 3);
});

test('matches a sorted-array oracle under random inserts and queries', () => {
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed;
  };
  const tree = new RedBlackTree<number>();
  const keys = new Set<number>();
  for (let i = 0; i < 20000; i += 1) {
    const key = rand() % 4294967296;
    keys.add(key);
    tree.insert(key, key);
  }
  const sorted = [...keys].sort((a, b) => a - b);
  assert.equal(tree.size, sorted.length);
  assert.equal(tree.minimum()?.key, sorted[0]);
  for (let i = 0; i < 20000; i += 1) {
    const query = rand() % 4294967296;
    let lo = 0;
    let hi = sorted.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (sorted[mid] < query) lo = mid + 1;
      else hi = mid;
    }
    const expected = lo < sorted.length ? sorted[lo] : null;
    assert.equal(tree.ceiling(query)?.key ?? null, expected);
  }
});
