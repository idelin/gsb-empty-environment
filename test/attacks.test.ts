import { describe, expect, test } from "bun:test";
import { hashesEqual } from "../src/hash";
import { MemoryChunkSource } from "../src/source";
import { verifyRange } from "../src/verifier";
import { CHUNK_SIZE, chunkOf, chunksFor, makeFile, proofsFor } from "./helpers";

describe("attack: swapping two equal-length chunks", () => {
  test("both swapped chunks are flagged bad", async () => {
    const f = await makeFile(6 * CHUNK_SIZE, 31);
    const chunks = chunksFor(f, [0, 1, 2, 3, 4, 5]);
    // Swap the payloads of chunks 1 and 4 (both full-length chunks).
    chunks.set(1, chunkOf(f, 4));
    chunks.set(4, chunkOf(f, 1));
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 6,
      chunks: new MemoryChunkSource(chunks),
      proofs: proofsFor(f, 0, 6), // genuine proofs for the genuine positions
    });
    expect(report.bad).toEqual([1, 4]);
    expect(report.ok).toEqual([0, 2, 3, 5]);
  });
});

describe("attack: modifying the total length", () => {
  test("commitment changes when length changes, even by one byte", async () => {
    const a = await makeFile(3 * CHUNK_SIZE + 7, 41);
    const b = await makeFile(3 * CHUNK_SIZE + 8, 41);
    expect(hashesEqual(a.proven.commitment, b.proven.commitment)).toBe(false);
  });

  test("a header with a tampered length fails the commitment check", async () => {
    const f = await makeFile(3 * CHUNK_SIZE + 7, 42);
    const forgedHeader = { ...f.proven.header, fileLength: 3 * CHUNK_SIZE + 8 };
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: forgedHeader,
      start: 0,
      end: 4,
      chunks: new MemoryChunkSource(chunksFor(f, [0, 1, 2, 3])),
      proofs: proofsFor(f, 0, 4),
    });
    expect(report.commitmentValid).toBe(false);
    // Nothing may be reported ok under a forged header — but unreceived
    // chunks are still "missing", and received ones "insufficient",
    // never silently "ok" and never mislabeled "bad".
    expect(report.ok).toEqual([]);
    expect(report.bad).toEqual([]);
    expect(report.insufficient).toEqual([0, 1, 2, 3]);
  });

  test("last-chunk proof from the untruncated file fails against the truncated commitment", async () => {
    const long = await makeFile(2 * CHUNK_SIZE + 7, 43);
    const short = await makeFile(2 * CHUNK_SIZE + 6, 43); // same prefix, last chunk 1 byte shorter
    // Attacker presents the long file's last chunk + proof against the short commitment.
    const chunks = new Map<number, Uint8Array>([
      [0, chunkOf(long, 0)],
      [1, chunkOf(long, 1)],
      [2, chunkOf(long, 2)],
    ]);
    const report = await verifyRange({
      commitment: short.proven.commitment,
      header: short.proven.header,
      start: 0,
      end: 3,
      chunks: new MemoryChunkSource(chunks),
      proofs: proofsFor(long, 0, 3),
    });
    // The length change re-pads the last chunk, which changes its leaf and
    // therefore every ancestor: even the identical prefix chunks cannot be
    // laundered across lengths with spliced proofs.
    expect(report.ok).toEqual([]);
    expect(report.bad).toEqual([0, 1, 2]);
  });
});

describe("attack: duplicated path nodes in a proof", () => {
  test("a proof with a duplicated sibling is rejected as non-canonical", async () => {
    const f = await makeFile(5 * CHUNK_SIZE, 51); // multi-level tree
    const proofs = proofsFor(f, 0, 5);
    const p0 = proofs.get(0)!;
    expect(p0.siblings.length).toBeGreaterThan(1);
    // Duplicate one path node: insert a copy of the first sibling.
    proofs.set(0, { index: 0, siblings: [p0.siblings[0], ...p0.siblings] });
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 5,
      chunks: new MemoryChunkSource(chunksFor(f, [0, 1, 2, 3, 4])),
      proofs,
    });
    expect(report.ok).toEqual([1, 2, 3, 4]);
    expect(report.insufficient).toEqual([0]); // malformed evidence, not "ok"
    expect(report.bad).toEqual([]);
  });

  test("a proof with the whole path duplicated is rejected", async () => {
    const f = await makeFile(5 * CHUNK_SIZE, 52);
    const proofs = proofsFor(f, 0, 5);
    const p1 = proofs.get(1)!;
    proofs.set(1, { index: 1, siblings: [...p1.siblings, ...p1.siblings] });
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 5,
      chunks: new MemoryChunkSource(chunksFor(f, [0, 1, 2, 3, 4])),
      proofs,
    });
    expect(report.insufficient).toEqual([1]);
    expect(report.ok).toEqual([0, 2, 3, 4]);
  });

  test("a proof for a promoted leaf padded with a duplicate of itself is rejected", async () => {
    const f = await makeFile(5 * CHUNK_SIZE, 53); // leaf 4 is promoted twice
    const proofs = proofsFor(f, 0, 5);
    const p4 = proofs.get(4)!;
    expect(p4.siblings.length).toBe(1);
    // Try to inflate it to look like a deeper path by duplicating its sibling.
    proofs.set(4, { index: 4, siblings: [p4.siblings[0], p4.siblings[0], p4.siblings[0]] });
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 4,
      end: 5,
      chunks: new MemoryChunkSource(chunksFor(f, [4])),
      proofs,
    });
    expect(report.ok).toEqual([]);
    expect(report.insufficient).toEqual([4]);
  });
});

describe("attack: splicing proofs across versions", () => {
  test("chunks from v2 with proofs from v1 are flagged bad", async () => {
    // Two versions, identical length and chunk size, differing in chunk 2.
    const v1data = 6 * CHUNK_SIZE;
    const v1 = await makeFile(v1data, 61);
    const modified = new Uint8Array(v1.data);
    modified[2 * CHUNK_SIZE + 5] ^= 0x01;
    const v2chunks = new Map<number, Uint8Array>();
    for (let i = 0; i < 6; i++) {
      v2chunks.set(i, modified.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE));
    }
    const { commitSource } = await import("../src/prover");
    const v2proven = await commitSource(new MemoryChunkSource(v2chunks), v1data, CHUNK_SIZE);
    expect(hashesEqual(v1.proven.commitment, v2proven.commitment)).toBe(false);

    // Spliced bundle: v2's data, v1's proofs, verified against v1's commitment.
    const report = await verifyRange({
      commitment: v1.proven.commitment,
      header: v1.proven.header,
      start: 0,
      end: 6,
      chunks: new MemoryChunkSource(v2chunks),
      proofs: proofsFor(v1, 0, 6),
    });
    expect(report.bad).toEqual([2]);
    expect(report.ok).toEqual([0, 1, 3, 4, 5]);
  });

  test("mixed bundle: v1 chunks verify, v2 chunks are caught individually", async () => {
    const v1 = await makeFile(6 * CHUNK_SIZE, 62);
    const modified = new Uint8Array(v1.data);
    modified[4 * CHUNK_SIZE] ^= 0x80;
    modified[5 * CHUNK_SIZE + 1] ^= 0x80;
    const chunks = new Map<number, Uint8Array>();
    for (let i = 0; i < 6; i++) {
      chunks.set(i, modified.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE));
    }
    // Attacker mixes: genuine v1 data+proofs for 0..3, v2 data with v1 proofs for 4..5.
    for (let i = 0; i < 4; i++) chunks.set(i, chunkOf(v1, i));
    const report = await verifyRange({
      commitment: v1.proven.commitment,
      header: v1.proven.header,
      start: 0,
      end: 6,
      chunks: new MemoryChunkSource(chunks),
      proofs: proofsFor(v1, 0, 6),
    });
    expect(report.ok).toEqual([0, 1, 2, 3]);
    expect(report.bad).toEqual([4, 5]);
    expect(report.missing).toEqual([]);
    expect(report.insufficient).toEqual([]);
  });

  test("v2 proofs poison every path through the changed node", async () => {
    const v1 = await makeFile(4 * CHUNK_SIZE, 63);
    const modified = new Uint8Array(v1.data);
    modified[CHUNK_SIZE] ^= 0x02;
    const v2chunks = new Map<number, Uint8Array>();
    for (let i = 0; i < 4; i++) {
      v2chunks.set(i, modified.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE));
    }
    const { commitSource, proveRange } = await import("../src/prover");
    const v2proven = await commitSource(new MemoryChunkSource(v2chunks), 4 * CHUNK_SIZE, CHUNK_SIZE);
    const v2proofs = new Map(proveRange(v2proven, 0, 4).map((p) => [p.index, p]));

    // v1 data + v2 proofs, checked against the v1 commitment. Every proof
    // whose path contains the changed leaf (all except index 1's own) now
    // recomputes to the v2 root and is flagged.
    const report = await verifyRange({
      commitment: v1.proven.commitment,
      header: v1.proven.header,
      start: 0,
      end: 4,
      chunks: new MemoryChunkSource(chunksFor(v1, [0, 1, 2, 3])),
      proofs: v2proofs,
    });
    expect(report.bad).toEqual([0, 2, 3]);
    expect(report.ok).toEqual([1]);
  });
});
