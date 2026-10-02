import { test } from 'node:test';
import assert from 'node:assert/strict';
import { chunk } from '../src/chunker.ts';
import { buildMerkleTree } from '../src/merkle.ts';
import { diffChunks, planSync } from '../src/sync.ts';
import { randomBytes } from '../src/prng.ts';

const FILE_SIZE = 10 * 1024 * 1024;
const INSERT_LEN = 100;

function scenario() {
  const original = randomBytes(FILE_SIZE, 20261002);
  const at = FILE_SIZE / 2;
  const modified = Buffer.concat([
    original.subarray(0, at),
    randomBytes(INSERT_LEN, 777),
    original.subarray(at),
  ]);
  return { original, modified };
}

test('acceptance: 100-byte insert into 10MB file keeps >=99% of chunk hashes', () => {
  const { original, modified } = scenario();
  const oldChunks = chunk(original);
  const newChunks = chunk(modified);
  const diff = diffChunks(oldChunks, newChunks);

  assert.ok(
    diff.changed.length <= 2,
    `expected at most 2 affected chunks, got ${diff.changed.length}`,
  );
  assert.ok(
    diff.reuseRatio >= 0.99,
    `expected >=99% reuse, got ${(diff.reuseRatio * 100).toFixed(3)}%`,
  );
});

test('acceptance: incremental sync needs only changed chunks + merkle path', () => {
  const { original, modified } = scenario();
  const oldChunks = chunk(original);
  const newChunks = chunk(modified);
  const oldTree = buildMerkleTree(oldChunks.map((c) => c.hash));
  const newTree = buildMerkleTree(newChunks.map((c) => c.hash));

  assert.notEqual(oldTree.root, newTree.root);

  const plan = planSync(oldChunks, newChunks, newTree);
  assert.ok(plan.chunksToSend.length <= 2);
  assert.equal(plan.newRoot, newTree.root);
  // Each proof is O(log n): ~11 hashes for ~1280 leaves.
  for (const p of plan.proofs) {
    assert.ok(p.length <= Math.ceil(Math.log2(newChunks.length)) + 1);
  }
});
