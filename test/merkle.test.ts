import { describe, expect, test } from "bun:test";
import { hashesEqual } from "../src/hash";
import {
  chunkCount,
  chunkDataLength,
  computeCommitment,
  generateProof,
  hashLeaf,
  proofPathLength,
  verifyProof,
} from "../src/tree";
import { CHUNK_SIZE, makeBytes, makeFile } from "./helpers";

describe("tree geometry", () => {
  test("chunk count and last-chunk length", () => {
    expect(chunkCount(0, CHUNK_SIZE)).toBe(0);
    expect(chunkCount(1, CHUNK_SIZE)).toBe(1);
    expect(chunkCount(CHUNK_SIZE, CHUNK_SIZE)).toBe(1);
    expect(chunkCount(CHUNK_SIZE + 1, CHUNK_SIZE)).toBe(2);
    expect(chunkDataLength(3 * CHUNK_SIZE + 10, CHUNK_SIZE, 3)).toBe(10);
    expect(chunkDataLength(3 * CHUNK_SIZE + 10, CHUNK_SIZE, 2)).toBe(CHUNK_SIZE);
  });

  test("promotion keeps proof paths canonical and minimal", () => {
    // 5 leaves: levels 5 -> 3 -> 2 -> 1. Leaf 4 is promoted twice.
    expect(proofPathLength(5, 4)).toBe(1);
    expect(proofPathLength(5, 0)).toBe(3);
    expect(proofPathLength(1, 0)).toBe(0);
    expect(proofPathLength(2, 1)).toBe(1);
  });
});

describe("commitment binding", () => {
  test("root binds file length: appending a zero byte changes the commitment", async () => {
    const a = await makeFile(100, 7);
    const b = await makeFile(101, 7); // same prefix, one extra byte
    // Same first 100 bytes by construction (same seed).
    expect(a.data.slice(0, 100)).toEqual(b.data.slice(0, 100));
    expect(hashesEqual(a.proven.commitment, b.proven.commitment)).toBe(false);
  });

  test("root binds chunk size", async () => {
    const a = await makeFile(200, 3, 64);
    const b = await makeFile(200, 3, 128);
    expect(hashesEqual(a.proven.commitment, b.proven.commitment)).toBe(false);
  });

  test("commitment recomputation is stable", async () => {
    const f = await makeFile(500, 11);
    expect(hashesEqual(computeCommitment(f.proven.header), f.proven.commitment)).toBe(true);
  });
});

describe("fixed last-chunk padding", () => {
  test("last chunk is zero-padded to chunkSize before hashing", () => {
    const data = makeBytes(10, 5);
    const direct = hashLeaf(10, CHUNK_SIZE, 0, data);
    const padded = new Uint8Array(CHUNK_SIZE);
    padded.set(data);
    // Recompute manually with the same fixed rule.
    const manual = hashLeaf(CHUNK_SIZE, CHUNK_SIZE, 0, padded);
    expect(hashesEqual(direct, manual)).toBe(true);
  });

  test("short non-last chunk data is rejected", () => {
    expect(() => hashLeaf(2 * CHUNK_SIZE, CHUNK_SIZE, 0, makeBytes(3, 1))).toThrow();
  });
});

describe("proofs", () => {
  test("every leaf of an odd-sized tree verifies", async () => {
    const f = await makeFile(5 * CHUNK_SIZE + 3, 9); // 6 chunks
    const count = chunkCount(f.data.length, CHUNK_SIZE);
    for (let i = 0; i < count; i++) {
      const proof = generateProof(f.proven.levels, i);
      expect(proof.siblings.length).toBe(proofPathLength(count, i));
      const leaf = hashLeaf(f.data.length, CHUNK_SIZE, i,
        f.data.slice(i * CHUNK_SIZE, Math.min(f.data.length, (i + 1) * CHUNK_SIZE)));
      expect(verifyProof(count, f.proven.header.treeRoot, leaf, proof).ok).toBe(true);
    }
  });

  test("empty file has a well-defined root and no chunks", async () => {
    const f = await makeFile(0, 1);
    expect(f.proven.leafCount).toBe(0);
    expect(f.proven.header.treeRoot.length).toBe(32);
  });
});
