import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { HashTree, BufferReader, type RandomAccessReader } from '../src/tree.ts';
import { verifyRange, type ChunkStatus } from '../src/verify.ts';
import { numLeaves } from '../src/merkle.ts';

const CHUNK = 1024;

function makeFile(seed: string, length: number): Buffer {
  // Deterministic pseudo-random content, not aligned to chunk boundaries.
  const out = Buffer.alloc(length);
  let pos = 0;
  let counter = 0;
  while (pos < length) {
    const block = createHash('sha256').update(`${seed}:${counter++}`).digest();
    const n = Math.min(block.length, length - pos);
    block.copy(out, pos, 0, n);
    pos += n;
  }
  return out;
}

/** Reader that records every read so tests can assert access patterns. */
class CountingReader implements RandomAccessReader {
  reads: Array<{ offset: number; length: number }> = [];
  private readonly inner: BufferReader;
  constructor(buf: Buffer) {
    this.inner = new BufferReader(buf);
  }
  get byteLength(): number {
    return this.inner.byteLength;
  }
  read(offset: number, length: number): Buffer {
    this.reads.push({ offset, length });
    return this.inner.read(offset, length);
  }
}

function statuses(report: { chunks: Array<{ status: ChunkStatus }> }): ChunkStatus[] {
  return report.chunks.map((c) => c.status);
}

const FILE_LEN = 10 * CHUNK + 300; // 11 chunks, short last chunk
const file = makeFile('v1', FILE_LEN);
const tree = HashTree.build(new BufferReader(file), CHUNK);

function prove(start: number, end: number) {
  return tree.proveRange(new BufferReader(file), start, end);
}

function verify(start: number, end: number, chunks: ReadonlyMap<number, Buffer>, proof: any) {
  return verifyRange({
    commitment: tree.commitment,
    fileLength: tree.fileLength,
    chunkSize: tree.chunkSize,
    start,
    end,
    chunks,
    proof,
  });
}

test('full file verifies chunk by chunk over arbitrary ranges', () => {
  for (const [start, end] of [[0, FILE_LEN], [0, CHUNK], [CHUNK, 3 * CHUNK], [5000, 9000], [FILE_LEN - 300, FILE_LEN]]) {
    const { proof, chunks } = prove(start, end);
    const report = verify(start, end, chunks, proof);
    assert.ok(report.ok, `range [${start}, ${end}) failed: ${JSON.stringify(report.chunks)}`);
    assert.ok(statuses(report).every((s) => s === 'verified'));
  }
});

test('range verification needs only the chunks in the range', () => {
  const { proof, chunks } = prove(2 * CHUNK, 3 * CHUNK);
  assert.equal(chunks.size, 1);
  const report = verify(2 * CHUNK, 3 * CHUNK, chunks, proof);
  assert.ok(report.ok);
});

test('prover reads only the requested blocks, not the whole file', () => {
  const reader = new CountingReader(file);
  const start = 3 * CHUNK;
  const end = 5 * CHUNK;
  tree.proveRange(reader, start, end);
  const readBytes = reader.reads.reduce((n, r) => n + r.length, 0);
  assert.equal(readBytes, 2 * CHUNK);
  for (const r of reader.reads) {
    assert.ok(r.offset >= start && r.offset + r.length <= end, `unexpected read ${JSON.stringify(r)}`);
  }
});

test('swapping two equal-length blocks is detected as corruption', () => {
  const swapped = Buffer.from(file);
  const a = 2 * CHUNK;
  const b = 7 * CHUNK;
  const tmp = Buffer.from(swapped.subarray(a, a + CHUNK));
  swapped.copyWithin(a, b, b + CHUNK);
  tmp.copy(swapped, b);

  const reader = new BufferReader(swapped);
  // Serve chunks from the swapped file, but proof from the honest tree.
  const { proof } = prove(0, FILE_LEN);
  const chunks = new Map<number, Buffer>();
  for (let i = 0; i < tree.leafCount; i++) {
    const off = i * CHUNK;
    chunks.set(i, reader.read(off, Math.min(CHUNK, FILE_LEN - off)));
  }
  const report = verify(0, FILE_LEN, chunks, proof);
  assert.equal(report.chunks[2].status, 'corrupt');
  assert.equal(report.chunks[7].status, 'corrupt');
  assert.ok(report.chunks.filter((c) => c.status === 'corrupt').length === 2);
  assert.ok(!report.ok);
});

test('modifying the total length changes the commitment and fails verification', () => {
  const truncated = file.subarray(0, FILE_LEN - 100);
  const treeTrunc = HashTree.build(new BufferReader(Buffer.from(truncated)), CHUNK);
  assert.notEqual(treeTrunc.commitment.toString('hex'), tree.commitment.toString('hex'));

  // Attacker serves the truncated file with its own honest proof, but the
  // verifier holds the original trusted commitment/metadata.
  const { proof, chunks } = treeTrunc.proveRange(new BufferReader(Buffer.from(truncated)), 0, truncated.length);
  const report = verify(0, truncated.length, chunks, proof);
  assert.ok(!report.ok);
  assert.ok(statuses(report).every((s) => s === 'insufficient-evidence'));

  // Attacker keeps original metadata but serves a shortened last chunk.
  const lastIdx = tree.leafCount - 1;
  const honest = prove(lastIdx * CHUNK, FILE_LEN);
  const shortChunks = new Map<number, Buffer>([[lastIdx, honest.chunks.get(lastIdx)!.subarray(0, 200)]]);
  const report2 = verify(lastIdx * CHUNK, FILE_LEN, shortChunks, honest.proof);
  assert.equal(report2.chunks[0].status, 'corrupt');
});

test('duplicated path nodes in a proof are rejected, not trusted', () => {
  const { proof, chunks } = prove(0, 2 * CHUNK);
  const dup = { ...proof.nodes[0] };
  const tampered = { ...proof, nodes: [...proof.nodes, dup] };
  const report = verify(0, 2 * CHUNK, chunks, tampered);
  assert.ok(!report.ok);
  assert.ok(statuses(report).every((s) => s === 'insufficient-evidence'));
});

test('cross-version proof splicing never verifies', () => {
  // Same length, different content.
  const fileV2 = makeFile('v2', FILE_LEN);
  const treeV2 = HashTree.build(new BufferReader(fileV2), CHUNK);
  assert.notEqual(treeV2.commitment.toString('hex'), tree.commitment.toString('hex'));

  // Chunks from v2 spliced with the v1 proof, checked against the v1 root.
  const v1 = prove(0, FILE_LEN);
  const v2 = treeV2.proveRange(new BufferReader(fileV2), 0, FILE_LEN);
  const spliced = verify(0, FILE_LEN, v2.chunks, v1.proof);
  assert.ok(!spliced.ok);
  assert.ok(statuses(spliced).every((s) => s === 'corrupt'));

  // Proof from v2 spliced with v1 chunks, checked against the v1 root.
  const spliced2 = verify(0, FILE_LEN, v1.chunks, v2.proof);
  assert.ok(!spliced2.ok);
  assert.ok(statuses(spliced2).every((s) => s === 'corrupt'));

  // Proof produced with a different chunk size (cross-parameter splicing).
  const treeOther = HashTree.build(new BufferReader(file), 2 * CHUNK);
  const other = treeOther.proveRange(new BufferReader(file), 0, 2 * CHUNK);
  const spliced3 = verify(0, 2 * CHUNK, v1.chunks, other.proof);
  assert.ok(!spliced3.ok);
  assert.ok(statuses(spliced3).every((s) => s === 'insufficient-evidence'));
});

test('missing chunks are reported as missing, never as corrupt', () => {
  const { proof, chunks } = prove(0, 4 * CHUNK);
  chunks.delete(1);
  const report = verify(0, 4 * CHUNK, chunks, proof);
  assert.equal(report.chunks[1].status, 'missing');
  assert.equal(report.chunks[0].status, 'verified');
  assert.ok(!report.ok);
  assert.ok(!statuses(report).includes('corrupt'));
});

test('absent or incomplete proof yields insufficient-evidence, not corrupt', () => {
  const { proof, chunks } = prove(0, 2 * CHUNK);

  const noProof = verify(0, 2 * CHUNK, chunks, null);
  assert.ok(statuses(noProof).every((s) => s === 'insufficient-evidence'));

  const partial = { ...proof, nodes: proof.nodes.slice(1) };
  const report = verify(0, 2 * CHUNK, chunks, partial);
  assert.ok(!report.ok);
  for (const c of report.chunks) {
    assert.notEqual(c.status, 'corrupt');
  }
});

test('single-bit tampering in a received chunk is corrupt', () => {
  const { proof, chunks } = prove(CHUNK, 2 * CHUNK);
  const bad = Buffer.from(chunks.get(1)!);
  bad[10] ^= 1;
  const report = verify(CHUNK, 2 * CHUNK, new Map([[1, bad]]), proof);
  assert.equal(report.chunks[0].status, 'corrupt');
});

test('empty file and single-chunk file edge cases', () => {
  const empty = HashTree.build(new BufferReader(Buffer.alloc(0)), CHUNK);
  const r0 = verifyRange({
    commitment: empty.commitment,
    fileLength: 0,
    chunkSize: CHUNK,
    start: 0,
    end: 0,
    chunks: new Map(),
    proof: null,
  });
  assert.ok(r0.ok);
  assert.equal(r0.chunks.length, 0);

  const tiny = makeFile('tiny', 17);
  const treeTiny = HashTree.build(new BufferReader(tiny), CHUNK);
  assert.equal(numLeaves(17, CHUNK), 1);
  const { proof, chunks } = treeTiny.proveRange(new BufferReader(tiny), 0, 17);
  const r1 = verifyRange({
    commitment: treeTiny.commitment,
    fileLength: 17,
    chunkSize: CHUNK,
    start: 0,
    end: 17,
    chunks,
    proof,
  });
  assert.ok(r1.ok);
});

test('odd leaf counts (promotion rule) verify correctly', () => {
  for (const leaves of [3, 5, 7, 9, 11]) {
    const len = leaves * CHUNK;
    const f = makeFile(`odd-${leaves}`, len);
    const t = HashTree.build(new BufferReader(f), CHUNK);
    const { proof, chunks } = t.proveRange(new BufferReader(f), 0, len);
    const report = verifyRange({
      commitment: t.commitment,
      fileLength: len,
      chunkSize: CHUNK,
      start: 0,
      end: len,
      chunks,
      proof,
    });
    assert.ok(report.ok, `leaves=${leaves}`);
  }
});
