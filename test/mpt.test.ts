import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MerklePatriciaTrie } from '../src/trie.ts';
import { verifyProof } from '../src/proof.ts';
import type { Proof } from '../src/proof.ts';
import type { BranchNode } from '../src/nodes.ts';

const ENTRIES: [string, string][] = [
  ['account.balance', '1000'],
  ['account.nonce', '7'],
  ['account.codeHash', '0xabc123'],
  ['account.storage.root', '0xdeadbeef'],
  ['block.number', '18234567'],
  ['block.hash', '0xfeedface'],
];

function buildTrie(): MerklePatriciaTrie {
  const trie = new MerklePatriciaTrie();
  for (const [k, v] of ENTRIES) trie.insert(k, v);
  return trie;
}

test('insert and get roundtrip for keys with common prefixes', () => {
  const trie = buildTrie();
  for (const [k, v] of ENTRIES) {
    assert.equal(trie.get(k)?.toString('utf8'), v);
  }
  assert.equal(trie.get('account.missing'), null);
  assert.equal(trie.get('nonexistent'), null);
});

test('shared prefixes are compressed into extension nodes', () => {
  const trie = buildTrie();
  const printed = trie.prettyPrint();
  // 'account.' and 'block.' share long prefixes -> extension nodes exist
  assert.match(printed, /Ext {2}path=/);
  // far fewer nodes than uncompressed (2 nibbles per char * total key length)
  const nodeCount = printed.split('\n').length;
  const uncompressedUpperBound = ENTRIES.reduce((n, [k]) => n + k.length * 2, 0);
  assert.ok(
    nodeCount < uncompressedUpperBound / 4,
    `expected heavy compression, got ${nodeCount} nodes`,
  );
});

test('root hash is deterministic and cascades on any change', () => {
  const a = buildTrie();
  const b = buildTrie();
  assert.equal(a.rootHash, b.rootHash, 'same inserts -> same root hash');

  const before = a.rootHash;
  a.insert('account.balance', '1001'); // single value change
  assert.notEqual(a.rootHash, before, 'root hash must change');

  const c = buildTrie();
  c.insert('zzz', '1'); // unrelated key also changes root
  assert.notEqual(c.rootHash, before);
});

test('range summary covers all keys', () => {
  const trie = buildTrie();
  const summary = trie.summary()!;
  assert.equal(summary.size, ENTRIES.length);
  assert.equal(summary.minKey, 'account.balance');
  assert.equal(summary.maxKey, 'block.number');
});

test('prefix retrieval returns sorted matching pairs', () => {
  const trie = buildTrie();
  const accountKeys = trie.keysWithPrefix('account.').map(([k]) => k);
  assert.deepEqual(accountKeys, [
    'account.balance',
    'account.codeHash',
    'account.nonce',
    'account.storage.root',
  ]);
  assert.equal(trie.keysWithPrefix('account.no')[0][1].toString(), '7');
  assert.equal(trie.keysWithPrefix('cat').length, 0);
});

test('proof for account.balance verifies and spans root -> leaf', () => {
  const trie = buildTrie();
  const proof = trie.getProof('account.balance');
  assert.equal(proof.nodes[0].hash, trie.rootHash);
  assert.equal(proof.nodes.at(-1)!.node.kind, 'leaf');
  assert.equal(
    Buffer.from(proof.value!, 'hex').toString('utf8'),
    '1000',
  );
  const result = verifyProof(proof);
  assert.equal(result.valid, true, result.reason);
});

test('tampering with the root hash is detected', () => {
  const trie = buildTrie();
  const proof = trie.getProof('account.balance');
  const tampered: Proof = { ...proof, rootHash: 'ff' + proof.rootHash.slice(2) };
  const result = verifyProof(tampered);
  assert.equal(result.valid, false);
  assert.match(result.reason!, /root hash/);
});

test('tampering with a parent branch node is detected', () => {
  const trie = buildTrie();
  const proof = structuredClone(trie.getProof('account.balance'));
  const idx = proof.nodes.findIndex((n) => n.node.kind === 'branch');
  assert.ok(idx >= 0, 'proof should contain a branch node');
  const branch = proof.nodes[idx].node as BranchNode;
  const slot = branch.children.findIndex((c) => c !== null);
  branch.children[slot] = 'ab'.repeat(32); // forge a child pointer
  const result = verifyProof(proof);
  assert.equal(result.valid, false);
});

test('tampering with the leaf value is detected', () => {
  const trie = buildTrie();
  const proof = structuredClone(trie.getProof('account.balance'));
  const leaf = proof.nodes.at(-1)!;
  assert.equal(leaf.node.kind, 'leaf');
  (leaf.node as { value: Buffer }).value = Buffer.from('999999');
  const result = verifyProof(proof);
  assert.equal(result.valid, false);
});

test('tampering with a sibling range summary is detected', () => {
  const trie = buildTrie();
  const proof = structuredClone(trie.getProof('account.balance'));
  const withSibling = proof.nodes.find((n) =>
    n.childSummaries.some((s) => s !== null),
  )!;
  const sibling = withSibling.childSummaries.find((s) => s !== null)!;
  sibling.summary = { ...sibling.summary, size: sibling.summary.size + 100 };
  const result = verifyProof(proof);
  assert.equal(result.valid, false);
});

test('value update under same key keeps other keys intact', () => {
  const trie = buildTrie();
  trie.insert('account.balance', '2000');
  assert.equal(trie.get('account.balance')?.toString(), '2000');
  assert.equal(trie.get('account.nonce')?.toString(), '7');
  assert.equal(trie.get('block.hash')?.toString(), '0xfeedface');
  assert.equal(trie.summary()!.size, ENTRIES.length);
});
