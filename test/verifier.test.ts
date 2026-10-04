import { describe, expect, test } from "bun:test";
import { MemoryChunkSource } from "../src/source";
import { verifyRange } from "../src/verifier";
import { chunkCount } from "../src/tree";
import { CHUNK_SIZE, chunksFor, makeFile, proofsFor } from "./helpers";

describe("range verification", () => {
  test("fully provided range verifies ok", async () => {
    const f = await makeFile(10 * CHUNK_SIZE + 5, 21);
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 2,
      end: 8,
      chunks: new MemoryChunkSource(chunksFor(f, [2, 3, 4, 5, 6, 7])),
      proofs: proofsFor(f, 2, 8),
    });
    expect(report.commitmentValid).toBe(true);
    expect(report.ok).toEqual([2, 3, 4, 5, 6, 7]);
    expect(report.bad).toEqual([]);
    expect(report.missing).toEqual([]);
    expect(report.insufficient).toEqual([]);
  });

  test("unreceived chunks are reported missing, never corrupt", async () => {
    const f = await makeFile(8 * CHUNK_SIZE, 22);
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 8,
      chunks: new MemoryChunkSource(chunksFor(f, [1, 4])), // rest unreceived
      proofs: proofsFor(f, 0, 8),
    });
    expect(report.missing).toEqual([0, 2, 3, 5, 6, 7]);
    expect(report.ok).toEqual([1, 4]);
    expect(report.bad).toEqual([]); // absence is not corruption
  });

  test("data without proof is insufficient evidence, not corruption", async () => {
    const f = await makeFile(4 * CHUNK_SIZE, 23);
    const proofs = proofsFor(f, 0, 4);
    proofs.delete(2);
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 4,
      chunks: new MemoryChunkSource(chunksFor(f, [0, 1, 2, 3])),
      proofs,
    });
    expect(report.insufficient).toEqual([2]);
    expect(report.bad).toEqual([]);
    expect(report.ok).toEqual([0, 1, 3]);
  });

  test("range is clamped to the actual chunk count", async () => {
    const f = await makeFile(2 * CHUNK_SIZE, 24);
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 999,
      chunks: new MemoryChunkSource(chunksFor(f, [0, 1])),
      proofs: proofsFor(f, 0, 2),
    });
    expect(report.end).toBe(2);
    expect(report.ok).toEqual([0, 1]);
  });

  test("corrupted chunk bytes are reported bad", async () => {
    const f = await makeFile(4 * CHUNK_SIZE, 25);
    const chunks = chunksFor(f, [0, 1, 2, 3]);
    const tampered = new Uint8Array(chunks.get(1)!);
    tampered[0] ^= 0xff;
    chunks.set(1, tampered);
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 4,
      chunks: new MemoryChunkSource(chunks),
      proofs: proofsFor(f, 0, 4),
    });
    expect(report.bad).toEqual([1]);
    expect(report.ok).toEqual([0, 2, 3]);
  });

  test("wrong-sized chunk data is bad, not merely insufficient", async () => {
    const f = await makeFile(4 * CHUNK_SIZE, 26);
    const chunks = chunksFor(f, [0, 1, 2, 3]);
    chunks.set(3, chunks.get(3)!.slice(0, 10)); // truncated
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 4,
      chunks: new MemoryChunkSource(chunks),
      proofs: proofsFor(f, 0, 4),
    });
    expect(report.bad).toEqual([3]);
  });

  test("empty range and empty file are trivially clean", async () => {
    const f = await makeFile(0, 27);
    const report = await verifyRange({
      commitment: f.proven.commitment,
      header: f.proven.header,
      start: 0,
      end: 0,
      chunks: new MemoryChunkSource(new Map()),
      proofs: new Map(),
    });
    expect(report.commitmentValid).toBe(true);
    expect(report.ok).toEqual([]);
    expect(report.bad).toEqual([]);
    expect(chunkCount(0, CHUNK_SIZE)).toBe(0);
  });
});
