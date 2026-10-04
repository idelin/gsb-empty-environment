import {
  COMMITMENT_DOMAIN,
  EMPTY_TREE_PREFIX,
  Hash,
  HASH_LENGTH,
  LEAF_PREFIX,
  NODE_PREFIX,
  hashesEqual,
  sha256,
  u32le,
  u64le,
} from "./hash";

/**
 * Hash-tree layout (version 1, fixed rules):
 *
 * - The file is split into ceil(length / chunkSize) chunks.
 * - The last chunk is ZERO-PADDED up to chunkSize before hashing (fixed
 *   padding rule). Every other chunk is already exactly chunkSize bytes.
 * - Leaf i   = H(0x00 || u64le(i) || paddedChunk_i)
 * - Node     = H(0x01 || left || right)
 * - Odd node counts: the last node is PROMOTED unchanged to the next level
 *   (never duplicated), which makes duplicate-path-node proofs non-canonical.
 * - Empty file: treeRoot = H(0x02).
 * - Commitment = H("MRV1" || u64le(fileLength) || u32le(chunkSize) || treeRoot)
 *   which binds the root to the exact file length and chunk size.
 */

export interface CommitmentHeader {
  fileLength: number;
  chunkSize: number;
  treeRoot: Hash;
}

export interface ChunkProof {
  /** Chunk (leaf) index this proof belongs to. */
  index: number;
  /** Sibling hashes from the leaf level upward. Promoted levels contribute no entry. */
  siblings: Hash[];
}

export function chunkCount(fileLength: number, chunkSize: number): number {
  if (fileLength < 0 || chunkSize <= 0) throw new Error("invalid geometry");
  return Math.ceil(fileLength / chunkSize);
}

/** Number of bytes of real data in chunk `index` (chunkSize, or less for the last chunk). */
export function chunkDataLength(fileLength: number, chunkSize: number, index: number): number {
  const count = chunkCount(fileLength, chunkSize);
  if (index < 0 || index >= count) throw new Error(`chunk index ${index} out of range`);
  if (fileLength === 0) return 0;
  const start = index * chunkSize;
  return Math.min(chunkSize, fileLength - start);
}

/** Hash a chunk's real (unpadded) bytes, applying the fixed zero-padding rule. */
export function hashLeaf(
  fileLength: number,
  chunkSize: number,
  index: number,
  data: Uint8Array,
): Hash {
  const expected = chunkDataLength(fileLength, chunkSize, index);
  if (data.length !== expected) {
    throw new Error(
      `chunk ${index} must carry exactly ${expected} bytes, got ${data.length}`,
    );
  }
  const padded = new Uint8Array(chunkSize); // zero-filled: fixed padding rule
  padded.set(data);
  return sha256(new Uint8Array([LEAF_PREFIX]), u64le(index), padded);
}

export function hashNode(left: Hash, right: Hash): Hash {
  return sha256(new Uint8Array([NODE_PREFIX]), left, right);
}

export function emptyTreeRoot(): Hash {
  return sha256(new Uint8Array([EMPTY_TREE_PREFIX]));
}

/** Build every tree level above the leaves. levels[0] is the leaf level. */
export function buildLevels(leaves: Hash[]): Hash[][] {
  const levels: Hash[][] = [leaves.slice()];
  let current = leaves;
  while (current.length > 1) {
    const next: Hash[] = [];
    for (let i = 0; i < current.length; i += 2) {
      if (i + 1 < current.length) {
        next.push(hashNode(current[i], current[i + 1]));
      } else {
        next.push(current[i]); // promoted, never duplicated
      }
    }
    levels.push(next);
    current = next;
  }
  return levels;
}

export function treeRootFromLeaves(leaves: Hash[]): Hash {
  if (leaves.length === 0) return emptyTreeRoot();
  const levels = buildLevels(leaves);
  return levels[levels.length - 1][0];
}

/**
 * Number of sibling hashes a canonical proof for `index` carries.
 * With odd-count promotion this varies per index; any proof with a
 * different length (e.g. containing duplicated path nodes) is non-canonical.
 */
export function proofPathLength(leafCount: number, index: number): number {
  if (index < 0 || index >= leafCount) throw new Error("leaf index out of range");
  let length = 0;
  let n = leafCount;
  let i = index;
  while (n > 1) {
    const isPromoted = n % 2 === 1 && i === n - 1;
    if (!isPromoted) length++;
    i = Math.floor(i / 2);
    n = Math.ceil(n / 2);
  }
  return length;
}

/** Generate a canonical proof for leaf `index` from the tree levels. */
export function generateProof(levels: Hash[][], index: number): ChunkProof {
  const leafCount = levels[0].length;
  if (index < 0 || index >= leafCount) throw new Error("leaf index out of range");
  const siblings: Hash[] = [];
  let i = index;
  for (let level = 0; level < levels.length - 1; level++) {
    const nodes = levels[level];
    const isPromoted = nodes.length % 2 === 1 && i === nodes.length - 1;
    if (!isPromoted) {
      siblings.push(nodes[i ^ 1]);
    }
    i = Math.floor(i / 2);
  }
  return { index, siblings };
}

export type ProofCheck =
  | { ok: true }
  | { ok: false; reason: "malformed" | "root-mismatch" };

/**
 * Verify a leaf hash + proof against a trusted tree root.
 * A structurally non-canonical proof (wrong length, bad index) is "malformed";
 * a well-formed proof that recomputes to a different root is "root-mismatch".
 */
export function verifyProof(
  leafCount: number,
  treeRoot: Hash,
  leafHash: Hash,
  proof: ChunkProof,
): ProofCheck {
  if (proof.index < 0 || proof.index >= leafCount) return { ok: false, reason: "malformed" };
  for (const s of proof.siblings) {
    if (s.length !== HASH_LENGTH) return { ok: false, reason: "malformed" };
  }
  let expected: number;
  try {
    expected = proofPathLength(leafCount, proof.index);
  } catch {
    return { ok: false, reason: "malformed" };
  }
  if (proof.siblings.length !== expected) return { ok: false, reason: "malformed" };

  let current = leafHash;
  let i = proof.index;
  let n = leafCount;
  let cursor = 0;
  while (n > 1) {
    const isPromoted = n % 2 === 1 && i === n - 1;
    if (!isPromoted) {
      const sibling = proof.siblings[cursor++];
      current = i % 2 === 0 ? hashNode(current, sibling) : hashNode(sibling, current);
    }
    i = Math.floor(i / 2);
    n = Math.ceil(n / 2);
  }
  return hashesEqual(current, treeRoot)
    ? { ok: true }
    : { ok: false, reason: "root-mismatch" };
}

/** Commitment = H(domain || fileLength || chunkSize || treeRoot). */
export function computeCommitment(header: CommitmentHeader): Hash {
  return sha256(
    new Uint8Array(Buffer.from(COMMITMENT_DOMAIN, "utf8")),
    u64le(header.fileLength),
    u32le(header.chunkSize),
    header.treeRoot,
  );
}

export function commitmentMatches(header: CommitmentHeader, commitment: Hash): boolean {
  return hashesEqual(computeCommitment(header), commitment);
}
