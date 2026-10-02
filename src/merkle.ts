import { createHash } from 'node:crypto';

export interface MerkleProofStep {
  hash: string;
  /** Position of the sibling relative to the running node. */
  side: 'left' | 'right';
}

export interface MerkleTree {
  /** Hex digest of the root node. */
  root: string;
  /** Leaf hashes in order (chunk hashes). */
  leaves: string[];
  /** Tree levels from leaves (index 0) up to the root. */
  levels: string[][];
}

function hashPair(left: string, right: string): string {
  return createHash('sha256')
    .update(Buffer.from(left + right, 'hex'))
    .digest('hex');
}

/**
 * Build a Merkle tree over chunk hashes. An odd node at any level is promoted
 * unchanged to the next level (no duplication), keeping proofs deterministic.
 */
export function buildMerkleTree(leaves: string[]): MerkleTree {
  if (leaves.length === 0) throw new Error('cannot build a tree with no leaves');
  const levels: string[][] = [leaves.slice()];
  let current = leaves.slice();
  while (current.length > 1) {
    const next: string[] = [];
    for (let i = 0; i < current.length; i += 2) {
      if (i + 1 < current.length) {
        next.push(hashPair(current[i], current[i + 1]));
      } else {
        next.push(current[i]);
      }
    }
    levels.push(next);
    current = next;
  }
  return { root: current[0], leaves: leaves.slice(), levels };
}

/** Authentication path for the leaf at `index`. */
export function getProof(tree: MerkleTree, index: number): MerkleProofStep[] {
  if (index < 0 || index >= tree.leaves.length) {
    throw new Error(`leaf index ${index} out of range`);
  }
  const proof: MerkleProofStep[] = [];
  let i = index;
  for (let level = 0; level < tree.levels.length - 1; level++) {
    const nodes = tree.levels[level];
    const sibling = i % 2 === 0 ? i + 1 : i - 1;
    if (sibling < nodes.length) {
      proof.push({
        hash: nodes[sibling],
        side: i % 2 === 0 ? 'right' : 'left',
      });
    }
    i = Math.floor(i / 2);
  }
  return proof;
}

/** Verify a leaf against a root using its authentication path. */
export function verifyProof(
  leaf: string,
  proof: MerkleProofStep[],
  root: string,
): boolean {
  let acc = leaf;
  for (const step of proof) {
    acc =
      step.side === 'right'
        ? hashPair(acc, step.hash)
        : hashPair(step.hash, acc);
  }
  return acc === root;
}
