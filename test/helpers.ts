import { MemoryChunkSource } from "../src/source";
import { commitSource, ProvenFile, proveRange } from "../src/prover";
import { ChunkProof } from "../src/tree";

export const CHUNK_SIZE = 64;

/** Deterministic pseudo-random bytes so tests are reproducible. */
export function makeBytes(length: number, seed = 1): Uint8Array {
  const out = new Uint8Array(length);
  let x = seed >>> 0 || 1;
  for (let i = 0; i < length; i++) {
    x ^= x << 13; x >>>= 0;
    x ^= x >> 17;
    x ^= x << 5; x >>>= 0;
    out[i] = x & 0xff;
  }
  return out;
}

export interface Fixture {
  data: Uint8Array;
  proven: ProvenFile;
  chunkSize: number;
}

export async function makeFile(length: number, seed = 1, chunkSize = CHUNK_SIZE): Promise<Fixture> {
  const data = makeBytes(length, seed);
  const chunks = new Map<number, Uint8Array>();
  const count = Math.ceil(length / chunkSize);
  for (let i = 0; i < count; i++) {
    chunks.set(i, data.slice(i * chunkSize, Math.min(length, (i + 1) * chunkSize)));
  }
  const proven = await commitSource(new MemoryChunkSource(chunks), length, chunkSize);
  return { data, proven, chunkSize };
}

export function chunkOf(f: Fixture, index: number): Uint8Array {
  const start = index * f.chunkSize;
  return f.data.slice(start, Math.min(f.data.length, start + f.chunkSize));
}

export function proofsFor(f: Fixture, start: number, end: number): Map<number, ChunkProof> {
  return new Map(proveRange(f.proven, start, end).map((p) => [p.index, p]));
}

export function chunksFor(f: Fixture, indices: number[]): Map<number, Uint8Array> {
  return new Map(indices.map((i) => [i, chunkOf(f, i)]));
}
