import type { Chunk } from './chunker.ts';
import { buildMerkleTree, getProof, type MerkleTree } from './merkle.ts';

export interface DiffResult {
  /** Chunks of the new version whose hash already exists in the old version. */
  reused: Chunk[];
  /** Chunks of the new version that must be transferred. */
  changed: Chunk[];
  /** reused / total ratio for the new version. */
  reuseRatio: number;
}

/**
 * Content-addressed diff: a chunk is reusable iff its SHA-256 already exists
 * in the old chunk set. Order independent — boundaries may shift freely.
 */
export function diffChunks(oldChunks: Chunk[], newChunks: Chunk[]): DiffResult {
  const oldHashes = new Set(oldChunks.map((c) => c.hash));
  const reused: Chunk[] = [];
  const changed: Chunk[] = [];
  for (const c of newChunks) {
    (oldHashes.has(c.hash) ? reused : changed).push(c);
  }
  return {
    reused,
    changed,
    reuseRatio: newChunks.length === 0 ? 1 : reused.length / newChunks.length,
  };
}

export interface SyncPlan {
  /** Chunk payloads that must be sent. */
  chunksToSend: Chunk[];
  /** Merkle proof (authentication path) for every changed chunk in the new tree. */
  proofs: ReturnType<typeof getProof>[];
  /** New Merkle root the receiver should reconstruct. */
  newRoot: string;
}

/**
 * Minimal incremental transfer plan: only the changed chunk payloads plus
 * their Merkle authentication paths in the new tree.
 */
export function planSync(
  oldChunks: Chunk[],
  newChunks: Chunk[],
  newTree?: MerkleTree,
): SyncPlan {
  const tree = newTree ?? buildMerkleTree(newChunks.map((c) => c.hash));
  const { changed } = diffChunks(oldChunks, newChunks);
  return {
    chunksToSend: changed,
    proofs: changed.map((c) => getProof(tree, c.index)),
    newRoot: tree.root,
  };
}
