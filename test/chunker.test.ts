import { test } from 'node:test';
import assert from 'node:assert/strict';
import { chunk, DEFAULTS, sha256Hex } from '../src/chunker.ts';
import { randomBytes } from '../src/prng.ts';

test('chunks cover the input exactly, in order', () => {
  const data = randomBytes(1_000_000, 10);
  const chunks = chunk(data);
  let offset = 0;
  for (const [i, c] of chunks.entries()) {
    assert.equal(c.index, i);
    assert.equal(c.offset, offset);
    assert.equal(c.length, c.data.length);
    assert.deepEqual(c.data, data.subarray(offset, offset + c.length));
    assert.equal(c.hash, sha256Hex(c.data));
    offset += c.length;
  }
  assert.equal(offset, data.length);
});

test('chunk sizes respect min/max bounds', () => {
  const data = randomBytes(2_000_000, 11);
  const chunks = chunk(data);
  for (const [i, c] of chunks.entries()) {
    assert.ok(c.length <= DEFAULTS.maxSize, `chunk ${i} too large`);
    // Only the final chunk may be smaller than minSize.
    if (i < chunks.length - 1) {
      assert.ok(c.length >= DEFAULTS.minSize, `chunk ${i} too small`);
    }
  }
  const avg = data.length / chunks.length;
  assert.ok(avg > DEFAULTS.minSize && avg < DEFAULTS.maxSize);
});

test('chunking is deterministic', () => {
  const data = randomBytes(500_000, 12);
  const a = chunk(data).map((c) => c.hash);
  const b = chunk(data).map((c) => c.hash);
  assert.deepEqual(a, b);
});

test('boundaries are content-defined, not offset-defined', () => {
  // Same content shifted by a prefix must reproduce the same interior boundary.
  const body = randomBytes(300_000, 13);
  const shifted = Buffer.concat([randomBytes(1000, 14), body]);
  const baseHashes = new Set(chunk(body).map((c) => c.hash));
  const shiftedChunks = chunk(shifted);
  const interior = shiftedChunks.filter((c) => c.offset >= 1000 + DEFAULTS.maxSize);
  const reused = interior.filter((c) => baseHashes.has(c.hash));
  assert.ok(
    reused.length / interior.length > 0.95,
    `expected >95% interior reuse after prefix shift, got ${reused.length}/${interior.length}`,
  );
});
