import { Hash } from "./hash";
import { ChunkSource } from "./source";
import {
  ChunkProof,
  CommitmentHeader,
  buildLevels,
  chunkCount,
  computeCommitment,
  emptyTreeRoot,
  generateProof,
  hashLeaf,
} from "./tree";

export interface ProvenFile {
  header: CommitmentHeader;
  commitment: Hash;
  leafCount: number;
  levels: Hash[][];
}

/**
 * Commit to a file by streaming it chunk by chunk from the source.
 * The whole file is never held in memory at once.
 */
export async function commitSource(
  source: ChunkSource,
  fileLength: number,
  chunkSize: number,
): Promise<ProvenFile> {
  const count = chunkCount(fileLength, chunkSize);
  const leaves: Hash[] = [];
  for (let i = 0; i < count; i++) {
    const data = await source.getChunk(i);
    if (data === undefined) throw new Error(`source is missing chunk ${i}`);
    leaves.push(hashLeaf(fileLength, chunkSize, i, data));
  }
  const levels = buildLevels(leaves);
  const treeRoot = count === 0 ? emptyTreeRoot() : levels[levels.length - 1][0];
  const header: CommitmentHeader = { fileLength, chunkSize, treeRoot };
  return { header, commitment: computeCommitment(header), leafCount: count, levels };
}

/** Produce canonical proofs for the chunk range [start, end). */
export function proveRange(file: ProvenFile, start: number, end: number): ChunkProof[] {
  const proofs: ChunkProof[] = [];
  const s = Math.max(0, start);
  const e = Math.min(end, file.leafCount);
  for (let i = s; i < e; i++) proofs.push(generateProof(file.levels, i));
  return proofs;
}
