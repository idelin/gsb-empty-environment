import { MerklePatriciaTrie } from '../src/trie.ts';
import { verifyProof } from '../src/proof.ts';
import type { Proof } from '../src/proof.ts';

const trie = new MerklePatriciaTrie();

const entries: [string, string][] = [
  ['account.balance', '1000'],
  ['account.nonce', '5'],
  ['account.storage.code', '0x60806040'],
  ['account.storage.slot0', '0x00'],
  ['account.storage.slot1', '0xff'],
  ['profile.name', 'alice'],
  ['profile.avatar', 'ipfs://Qm...'],
];

console.log('=== Inserting key/value pairs ===');
for (const [key, value] of entries) {
  trie.put(key, value);
  console.log(`put ${JSON.stringify(key)} = ${JSON.stringify(value)}`);
}

console.log('\n=== Trie structure (shared paths compressed into Extension nodes) ===');
console.log(trie.describe());

console.log('\n=== Root hash (Merkle root) ===');
console.log(trie.rootHash);

console.log('\n=== Range summary at root ===');
console.log(trie.summary);

console.log('\n=== Prefix search: "account.storage" ===');
for (const { key, value } of trie.getByPrefix('account.storage')) {
  console.log(`${key} = ${value.toString('utf8')}`);
}

console.log('\n=== Updating one value cascades to a new root hash ===');
const before = trie.rootHash;
trie.put('account.balance', '1500');
console.log(`before: ${before}`);
console.log(`after:  ${trie.rootHash}`);
console.log(`changed: ${before !== trie.rootHash}`);

console.log('\n=== Merkle proof for "account.balance" (root -> leaf) ===');
const proof = trie.prove('account.balance');
if (proof === null) throw new Error('proof missing');
proof.nodes.forEach((node, i) => {
  const indent = '  '.repeat(i);
  if (node.kind === 'leaf') {
    console.log(`${indent}[${i}] Leaf      path=${node.path} key=${JSON.stringify(node.key)}`);
  } else if (node.kind === 'extension') {
    console.log(`${indent}[${i}] Extension path=${node.path} child=${node.child.slice(0, 12)}..`);
  } else {
    console.log(`${indent}[${i}] Branch    children=${node.children.filter((c) => c !== null).length} slots used`);
  }
});
console.log(`proof length: ${proof.nodes.length} nodes`);
console.log(`verify against root hash: ${verifyProof(trie.rootHash!, proof)}`);

console.log('\n=== Tamper detection ===');
const tamperedParent: Proof = structuredClone(proof);
if (tamperedParent.nodes[1].kind === 'extension') {
  tamperedParent.nodes[1].child = '0'.repeat(64);
}
console.log(`tampered parent child-hash accepted: ${verifyProof(trie.rootHash!, tamperedParent)}`);

const tamperedSummary: Proof = structuredClone(proof);
tamperedSummary.nodes[0].summary = { count: 999, minKey: 'a', maxKey: 'z' };
console.log(`tampered range summary accepted:     ${verifyProof(trie.rootHash!, tamperedSummary)}`);

const tamperedValue: Proof = structuredClone(proof);
const last = tamperedValue.nodes[tamperedValue.nodes.length - 1];
if (last.kind === 'leaf') last.value = Buffer.from('999999').toString('base64');
console.log(`tampered leaf value accepted:        ${verifyProof(trie.rootHash!, tamperedValue)}`);
