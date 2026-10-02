import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { MerklePatriciaTrie } from '../src/trie.ts';
import { verifyProof } from '../src/proof.ts';
import type { Proof } from '../src/proof.ts';

const SAMPLE: [string, string][] = [
  ['account.balance', '1000'],
  ['account.nonce', '5'],
  ['account.storage.code', '0x60806040'],
  ['account.storage.slot0', '0x00'],
  ['account.storage.slot1', '0xff'],
  ['profile.name', 'alice'],
];

function sampleTrie(): MerklePatriciaTrie {
  const trie = new MerklePatriciaTrie();
  for (const [k, v] of SAMPLE) trie.put(k, v);
  return trie;
}

describe('MerklePatriciaTrie', () => {
  it('inserts and retrieves values by exact key', () => {
    const trie = sampleTrie();
    for (const [k, v] of SAMPLE) {
      assert.equal(trie.get(k)?.toString('utf8'), v);
    }
    assert.equal(trie.get('account.missing'), null);
    assert.equal(trie.get('account'), null);
    assert.equal(trie.get('account.balance.sheet'), null);
  });

  it('compresses shared prefixes into extension nodes', () => {
    const trie = new MerklePatriciaTrie();
    trie.put('account.balance', '1000');
    trie.put('account.nonce', '5');
    const description = trie.describe();
    assert.match(description, /^Extension path=6163636f756e742e/);
    assert.ok(description.includes('Branch'));
    assert.equal(trie.size, 2);
  });

  it('splits extension nodes when a diverging key arrives', () => {
    const trie = new MerklePatriciaTrie();
    trie.put('account.balance', '1000');
    trie.put('account.nonce', '5');
    const hashBefore = trie.rootHash;
    trie.put('profile.name', 'alice');
    assert.notEqual(trie.rootHash, hashBefore);
    assert.match(trie.describe(), /^Branch/);
    assert.equal(trie.get('account.balance')?.toString('utf8'), '1000');
    assert.equal(trie.get('profile.name')?.toString('utf8'), 'alice');
  });

  it('stores a value directly on a branch when a key is a prefix of another', () => {
    const trie = new MerklePatriciaTrie();
    trie.put('account', 'root-entry');
    trie.put('account.balance', '1000');
    assert.equal(trie.get('account')?.toString('utf8'), 'root-entry');
    assert.equal(trie.get('account.balance')?.toString('utf8'), '1000');
    assert.equal(trie.size, 2);
  });

  it('updates root hash on every mutation and is insertion-order independent', () => {
    const a = sampleTrie();
    const b = new MerklePatriciaTrie();
    for (const [k, v] of [...SAMPLE].reverse()) b.put(k, v);
    assert.equal(a.rootHash, b.rootHash);

    const before = a.rootHash;
    a.put('account.balance', '1500');
    assert.notEqual(a.rootHash, before);
    assert.equal(a.size, SAMPLE.length);

    const c = sampleTrie();
    c.put('account.balance', '1500');
    assert.equal(a.rootHash, c.rootHash);
  });

  it('tracks a range summary (count/min/max) at the root', () => {
    const trie = sampleTrie();
    assert.deepEqual(trie.summary, {
      count: SAMPLE.length,
      minKey: 'account.balance',
      maxKey: 'profile.name',
    });
    trie.put('zz.last', '1');
    assert.equal(trie.summary.maxKey, 'zz.last');
    assert.equal(trie.summary.count, SAMPLE.length + 1);
  });

  it('retrieves entries by key prefix', () => {
    const trie = sampleTrie();
    const storage = trie.getByPrefix('account.storage').map((e) => e.key);
    assert.deepEqual(storage, [
      'account.storage.code',
      'account.storage.slot0',
      'account.storage.slot1',
    ]);
    assert.equal(trie.getByPrefix('account').length, 5);
    assert.equal(trie.getByPrefix('profile').length, 1);
    assert.equal(trie.getByPrefix('nonexistent').length, 0);
    assert.equal(trie.getByPrefix('account.balance').length, 1);
  });

  it('produces a verifiable root-to-leaf proof for account.balance', () => {
    const trie = sampleTrie();
    const proof = trie.prove('account.balance');
    assert.ok(proof);
    assert.ok(proof.nodes.length >= 3, 'proof should span extension/branch/leaf');
    assert.equal(proof.nodes[0].kind, 'branch');
    assert.ok(proof.nodes.some((n) => n.kind === 'extension'));
    assert.equal(proof.nodes[proof.nodes.length - 1].kind, 'leaf');
    assert.equal(verifyProof(trie.rootHash!, proof), true);
  });

  it('returns null proof for missing keys', () => {
    const trie = sampleTrie();
    assert.equal(trie.prove('account.missing'), null);
  });

  it('rejects proofs with a tampered parent hash link', () => {
    const trie = sampleTrie();
    const proof = trie.prove('account.balance')!;
    const tampered: Proof = structuredClone(proof);
    const parent = tampered.nodes[1];
    if (parent.kind === 'extension') parent.child = '0'.repeat(64);
    else if (parent.kind === 'branch') parent.children[6] = '0'.repeat(64);
    assert.equal(verifyProof(trie.rootHash!, tampered), false);
  });

  it('rejects proofs with a tampered range summary', () => {
    const trie = sampleTrie();
    const proof = trie.prove('account.balance')!;
    const tampered: Proof = structuredClone(proof);
    tampered.nodes[0].summary = { count: 999, minKey: 'a', maxKey: 'z' };
    assert.equal(verifyProof(trie.rootHash!, tampered), false);
  });

  it('rejects proofs with a tampered leaf value or wrong root', () => {
    const trie = sampleTrie();
    const proof = trie.prove('account.balance')!;

    const badValue: Proof = structuredClone(proof);
    const leaf = badValue.nodes[badValue.nodes.length - 1];
    if (leaf.kind === 'leaf') leaf.value = Buffer.from('999999').toString('base64');
    assert.equal(verifyProof(trie.rootHash!, badValue), false);

    assert.equal(verifyProof('f'.repeat(64), proof), false);

    const wrongKey: Proof = structuredClone(proof);
    wrongKey.key = 'account.nonce';
    assert.equal(verifyProof(trie.rootHash!, wrongKey), false);
  });
});
