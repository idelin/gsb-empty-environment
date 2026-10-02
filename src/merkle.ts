import { createHash } from 'node:crypto';

/** One step of a Merkle inclusion proof, ordered from leaf to root. */
export interface ProofStep {
  /** Where the sibling hash sits relative to the running node. */
  position: 'left' | 'right';
  /** Sibling hash, hex encoded. */
  hash: string;
}

const LEAF_DOMAIN = Buffer.from([0x00]);
const NODE_DOMAIN = Buffer.from([0x01]);

export function sha256(...parts: Buffer[]): Buffer {
  const digest = createHash('sha256');
  for (const part of parts) digest.update(part);
  return digest.digest();
}

/** Domain-separated leaf hash: H(0x00 || data). */
export function hashLeaf(data: Buffer): Buffer {
  return sha256(LEAF_DOMAIN, data);
}

/** Domain-separated internal node hash: H(0x01 || left || right). */
export function hashNode(left: Buffer, right: Buffer): Buffer {
  return sha256(NODE_DOMAIN, left, right);
}

/** Hash of an empty (unset) leaf. */
export const EMPTY_LEAF_HASH: Buffer = hashLeaf(Buffer.alloc(0));

/**
 * Derives the leaf index implied by a proof path.
 * A sibling on the left means the running node was a right child (bit = 1).
 */
export function leafIndexFromProof(proof: ProofStep[]): number {
  let index = 0;
  for (let level = 0; level < proof.length; level++) {
    if (proof[level].position === 'left') index |= 1 << level;
  }
  return index;
}

/**
 * Dynamic Merkle tree over a power-of-two leaf array.
 * Leaves are updated in place; capacity doubles automatically on demand.
 */
export class MerkleTree {
  private leaves: Buffer[];
  private leafCount = 0;

  constructor(initialCapacity = 4) {
    let capacity = 1;
    while (capacity < initialCapacity) capacity *= 2;
    this.leaves = new Array<Buffer>(capacity).fill(EMPTY_LEAF_HASH);
  }

  get capacity(): number {
    return this.leaves.length;
  }

  get size(): number {
    return this.leafCount;
  }

  /** Inserts or replaces the leaf at `index`, growing the tree if needed. */
  setLeaf(index: number, data: Buffer): void {
    if (index < 0 || !Number.isInteger(index)) {
      throw new RangeError(`leaf index must be a non-negative integer, got ${index}`);
    }
    while (index >= this.leaves.length) this.grow();
    if (this.leaves[index].equals(EMPTY_LEAF_HASH)) this.leafCount++;
    this.leaves[index] = hashLeaf(data);
  }

  getRoot(): Buffer {
    let level = this.leaves.slice();
    while (level.length > 1) {
      const next: Buffer[] = [];
      for (let i = 0; i < level.length; i += 2) {
        next.push(hashNode(level[i], level[i + 1]));
      }
      level = next;
    }
    return level[0];
  }

  /** Returns the inclusion proof for the leaf at `index` (leaf-to-root order). */
  getProof(index: number): ProofStep[] {
    if (index < 0 || index >= this.leaves.length) {
      throw new RangeError(`leaf index ${index} out of range (capacity ${this.leaves.length})`);
    }
    const proof: ProofStep[] = [];
    let level = this.leaves.slice();
    let cursor = index;
    while (level.length > 1) {
      const siblingIndex = cursor ^ 1;
      proof.push({
        position: siblingIndex > cursor ? 'right' : 'left',
        hash: level[siblingIndex].toString('hex'),
      });
      const next: Buffer[] = [];
      for (let i = 0; i < level.length; i += 2) {
        next.push(hashNode(level[i], level[i + 1]));
      }
      level = next;
      cursor >>= 1;
    }
    return proof;
  }

  /**
   * Standalone inclusion check: folds `proof` over `leafData` and compares
   * the result with `expectedRoot`. Pure function of its arguments, so any
   * external party can run it without access to the tree.
   */
  static verifyProof(leafData: Buffer, proof: ProofStep[], expectedRoot: Buffer): boolean {
    let acc = hashLeaf(leafData);
    for (const step of proof) {
      const sibling = Buffer.from(step.hash, 'hex');
      acc = step.position === 'right' ? hashNode(acc, sibling) : hashNode(sibling, acc);
    }
    return acc.equals(expectedRoot);
  }

  private grow(): void {
    const doubled = new Array<Buffer>(this.leaves.length * 2).fill(EMPTY_LEAF_HASH);
    for (let i = 0; i < this.leaves.length; i++) doubled[i] = this.leaves[i];
    this.leaves = doubled;
  }
}
