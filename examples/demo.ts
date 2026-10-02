import { chunk } from '../src/chunker.ts';
import { buildMerkleTree } from '../src/merkle.ts';
import { diffChunks, planSync } from '../src/sync.ts';
import { randomBytes } from '../src/prng.ts';

const FILE_SIZE = 10 * 1024 * 1024; // 10 MB
const INSERT_AT = FILE_SIZE / 2;
const INSERT_LEN = 100;

// Deterministic 10 MB "file".
const original = randomBytes(FILE_SIZE, 20261002);

// Insert 100 bytes in the middle.
const inserted = randomBytes(INSERT_LEN, 777);
const modified = Buffer.concat([
  original.subarray(0, INSERT_AT),
  inserted,
  original.subarray(INSERT_AT),
]);

console.log('=== Rolling-Hash CDC + Merkle Tree Demo ===');
console.log(`file size:            ${original.length} bytes`);
console.log(`insert:               ${INSERT_LEN} bytes at offset ${INSERT_AT}`);

const t0 = performance.now();
const oldChunks = chunk(original);
const t1 = performance.now();
const newChunks = chunk(modified);
const t2 = performance.now();

const oldTree = buildMerkleTree(oldChunks.map((c) => c.hash));
const newTree = buildMerkleTree(newChunks.map((c) => c.hash));

const diff = diffChunks(oldChunks, newChunks);
const plan = planSync(oldChunks, newChunks, newTree);

const fmt = (n: number) => n.toLocaleString('en-US');
console.log('\n--- chunking ---');
console.log(`old chunks:           ${fmt(oldChunks.length)} (avg ${(original.length / oldChunks.length).toFixed(0)} B, chunked in ${(t1 - t0).toFixed(0)} ms)`);
console.log(`new chunks:           ${fmt(newChunks.length)} (re-chunked in ${(t2 - t1).toFixed(0)} ms)`);

console.log('\n--- diff ---');
console.log(`reused chunks:        ${fmt(diff.reused.length)} (${(diff.reuseRatio * 100).toFixed(2)}%)`);
console.log(`changed chunks:       ${fmt(diff.changed.length)}`);
for (const c of diff.changed) {
  console.log(`  -> chunk #${c.index} @ offset ${fmt(c.offset)}, ${fmt(c.length)} bytes`);
}

console.log('\n--- merkle ---');
console.log(`old root:             ${oldTree.root}`);
console.log(`new root:             ${newTree.root}`);
console.log(`tree depth:           ${newTree.levels.length} levels`);
console.log(`proof length/change:  ${plan.proofs[0]?.length ?? 0} hashes`);

console.log('\n--- incremental sync plan ---');
const payloadBytes = plan.chunksToSend.reduce((n, c) => n + c.length, 0);
const proofBytes = plan.proofs.reduce((n, p) => n + p.length * 32, 0);
console.log(`chunks to send:       ${plan.chunksToSend.length} (${fmt(payloadBytes)} bytes payload)`);
console.log(`merkle proofs:        ${fmt(proofBytes)} bytes`);
console.log(`total transfer:       ${fmt(payloadBytes + proofBytes + 32)} bytes of ${fmt(modified.length)} (${(((payloadBytes + proofBytes + 32) / modified.length) * 100).toFixed(4)}%)`);
