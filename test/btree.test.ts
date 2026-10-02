import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BTree } from '../src/btree.ts';

test('many records with mixed sizes survive leaf/internal splits', () => {
  const tree = new BTree();
  const expected = new Map<string, string>();
  for (let i = 0; i < 500; i++) {
    const key = `row:${String(i).padStart(4, '0')}`;
    // every 10th row overflows the inline threshold
    const value = i % 10 === 0 ? `big-${i}-` + 'v'.repeat(3000) : `val-${i}`;
    expected.set(key, value);
    tree.insert(key, value);
  }
  for (const [key, value] of expected) {
    assert.equal(tree.get(key)!.toString(), value, `mismatch for ${key}`);
  }
  assert.equal(tree.get('row:9999'), null);
});

test('random-order inserts keep every record retrievable', () => {
  const tree = new BTree();
  const keys: string[] = [];
  for (let i = 0; i < 200; i++) keys.push(`k${i}`);
  // deterministic shuffle
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  keys.sort(() => rand() - 0.5);
  for (const k of keys) tree.insert(k, `payload-${k}`);
  for (let i = 0; i < 200; i++) {
    assert.equal(tree.get(`k${i}`)!.toString(), `payload-k${i}`);
  }
});

test('duplicate insert replaces the value', () => {
  const tree = new BTree();
  tree.insert('a', '1');
  tree.insert('a', '2');
  assert.equal(tree.get('a')!.toString(), '2');
});
