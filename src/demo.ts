// Demo: build an MPT with shared-prefix keys, print the compressed tree,
// produce and verify a proof for 'account.balance', then show tamper detection.

import { MerklePatriciaTrie } from './trie.ts';
import { verifyProof } from './proof.ts';
import type { Proof } from './proof.ts';

const trie = new MerklePatriciaTrie();

const entries: [string, string][] = [
  ['account.balance', '1000'],
  ['account.nonce', '7'],
  ['account.codeHash', '0xabc123'],
  ['account.storage.root', '0xdeadbeef'],
  ['block.number', '18234567'],
  ['block.hash', '0xfeedface'],
];

console.log('=== Inserting key/value pairs ===');
for (const [key, value] of entries) {
  trie.insert(key, value);
  console.log(`put ${key} = ${value}  -> root ${trie.rootHash!.slice(0, 16)}…`);
}

console.log('\n=== Compressed tree (paths auto-shrunk into Extension/Leaf nodes) ===');
console.log(trie.prettyPrint());

const summary = trie.summary()!;
console.log('\n=== Root range summary ===');
console.log(`keys: ${summary.size}, range: [${summary.minKey} .. ${summary.maxKey}]`);

console.log('\n=== Prefix lookup: keysWithPrefix("account.") ===');
for (const [key, value] of trie.keysWithPrefix('account.')) {
  console.log(`${key} = ${value.toString('utf8')}`);
}

console.log('\n=== Inclusion proof for "account.balance" (root -> leaf) ===');
const proof = trie.getProof('account.balance');
proof.nodes.forEach((node, i) => {
  const desc =
    node.node.kind === 'branch'
      ? 'Branch'
      : node.node.kind === 'extension'
        ? `Ext path=${node.node.path.map((n) => n.toString(16)).join('')}`
        : `Leaf value=${node.node.value.toString('utf8')}`;
  console.log(`  [${i}] #${node.hash.slice(0, 16)}… ${desc}`);
});
console.log('verify:', verifyProof(proof));

console.log('\n=== Tamper 1: flip the root/parent hash ===');
const tamperedRoot: Proof = { ...proof, rootHash: 'ff' + proof.rootHash.slice(2) };
console.log('verify:', verifyProof(tamperedRoot));

console.log('\n=== Tamper 2: alter a parent branch node (child pointer) ===');
const tamperedParent: Proof = structuredClone(proof);
const branchIdx = tamperedParent.nodes.findIndex((n) => n.node.kind === 'branch');
if (branchIdx >= 0 && tamperedParent.nodes[branchIdx].node.kind === 'branch') {
  const branch = tamperedParent.nodes[branchIdx].node;
  const slot = branch.children.findIndex((c) => c !== null);
  branch.children[slot] = '00'.repeat(32);
}
console.log('verify:', verifyProof(tamperedParent));

console.log('\n=== Tamper 3: alter the leaf value ===');
const tamperedLeaf: Proof = structuredClone(proof);
const last = tamperedLeaf.nodes[tamperedLeaf.nodes.length - 1];
if (last.node.kind === 'leaf') last.node.value = Buffer.from('999999');
console.log('verify:', verifyProof(tamperedLeaf));
