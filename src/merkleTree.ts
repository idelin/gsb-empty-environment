import { createHash } from 'node:crypto';

const LEAF_PREFIX = Buffer.from([0x00]);
const NODE_PREFIX = Buffer.from([0x01]);

export function hashLeaf(data: Buffer): Buffer {
  return createHash('sha256').update(Buffer.concat([LEAF_PREFIX, data])).digest();
}

export function hashNode(left: Buffer, right: Buffer): Buffer {
  return createHash('sha256').update(Buffer.concat([NODE_PREFIX, left, right])).digest();
}

export interface ProofStep {
  position: 'left' | 'right';
  hash: string; // hex-encoded sibling hash
}

export interface MerkleProof {
  leafIndex: number;
  leafHash: string; // hex
  path: ProofStep[];
}

export function foldProof(leafHash: Buffer, path: ProofStep[]): Buffer {
  let current = leafHash;
  for (const step of path) {
    const sibling = Buffer.from(step.hash, 'hex');
    current =
      step.position === 'left' ? hashNode(sibling, current) : hashNode(current, sibling);
  }
  return current;
}

export function verifyProof(leafData: Buffer, proof: MerkleProof, expectedRoot: Buffer): boolean {
  const leafHash = hashLeaf(leafData);
  if (!leafHash.equals(Buffer.from(proof.leafHash, 'hex'))) return false;
  return foldProof(leafHash, proof.path).equals(expectedRoot);
}

/**
 * Dynamic Merkle tree over SHA-256 with domain-separated hashing.
 * Supports append and in-place update; odd levels duplicate the last node.
 */
export class MerkleTree {
  private leaves: Buffer[] = [];

  get size(): number {
    return this.leaves.length;
  }

  append(leafData: Buffer): number {
    this.leaves.push(hashLeaf(leafData));
    return this.leaves.length - 1;
  }

  update(index: number, leafData: Buffer): void {
    if (index < 0 || index >= this.leaves.length) {
      throw new RangeError(`leaf index ${index} out of range (size=${this.leaves.length})`);
    }
    this.leaves[index] = hashLeaf(leafData);
  }

  leafHashAt(index: number): Buffer {
    if (index < 0 || index >= this.leaves.length) {
      throw new RangeError(`leaf index ${index} out of range (size=${this.leaves.length})`);
    }
    return Buffer.from(this.leaves[index]);
  }

  root(): Buffer {
    if (this.leaves.length === 0) return hashLeaf(Buffer.alloc(0));
    let level = this.leaves.map((l) => Buffer.from(l));
    while (level.length > 1) {
      const next: Buffer[] = [];
      for (let i = 0; i < level.length; i += 2) {
        const left = level[i];
        const right = i + 1 < level.length ? level[i + 1] : level[i];
        next.push(hashNode(left, right));
      }
      level = next;
    }
    return level[0];
  }

  getProof(index: number): MerkleProof {
    if (index < 0 || index >= this.leaves.length) {
      throw new RangeError(`leaf index ${index} out of range (size=${this.leaves.length})`);
    }
    const path: ProofStep[] = [];
    let level = this.leaves.map((l) => Buffer.from(l));
    let idx = index;
    while (level.length > 1) {
      const siblingIdx = idx % 2 === 0 ? idx + 1 : idx - 1;
      const sibling = siblingIdx < level.length ? level[siblingIdx] : level[idx];
      path.push({
        position: idx % 2 === 0 ? 'right' : 'left',
        hash: sibling.toString('hex'),
      });
      const next: Buffer[] = [];
      for (let i = 0; i < level.length; i += 2) {
        const left = level[i];
        const right = i + 1 < level.length ? level[i + 1] : level[i];
        next.push(hashNode(left, right));
      }
      level = next;
      idx = Math.floor(idx / 2);
    }
    return {
      leafIndex: index,
      leafHash: this.leaves[index].toString('hex'),
      path,
    };
  }
}
