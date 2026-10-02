import { createHash } from 'node:crypto';

export interface MerklePathStep {
  position: 'left' | 'right';
  hash: string;
}

export interface MerkleProof {
  leaf: string;
  leafIndex: number;
  treeHeight: number;
  path: MerklePathStep[];
  root: string;
}

export function sha256Hex(data: string): string {
  return createHash('sha256').update(data, 'utf8').digest('hex');
}

function hashPair(left: string, right: string): string {
  return sha256Hex(left + right);
}

export function computeMerkleRoot(leaves: readonly string[]): string {
  if (leaves.length === 0) {
    return sha256Hex('');
  }
  let level: string[] = [...leaves];
  while (level.length > 1) {
    const next: string[] = [];
    for (let i = 0; i < level.length; i += 2) {
      const left = level[i];
      const right = i + 1 < level.length ? level[i + 1] : left;
      next.push(hashPair(left, right));
    }
    level = next;
  }
  return level[0];
}

export function getMerklePath(leaves: readonly string[], index: number): MerklePathStep[] {
  if (index < 0 || index >= leaves.length) {
    throw new RangeError(`leaf index ${index} out of range (leaves: ${leaves.length})`);
  }
  const path: MerklePathStep[] = [];
  let level: string[] = [...leaves];
  let cursor = index;
  while (level.length > 1) {
    const sibling = cursor % 2 === 0 ? cursor + 1 : cursor - 1;
    if (sibling < level.length) {
      path.push({
        position: cursor % 2 === 0 ? 'right' : 'left',
        hash: level[sibling],
      });
    } else {
      path.push({ position: 'right', hash: level[cursor] });
    }
    const next: string[] = [];
    for (let i = 0; i < level.length; i += 2) {
      const left = level[i];
      const right = i + 1 < level.length ? level[i + 1] : left;
      next.push(hashPair(left, right));
    }
    level = next;
    cursor = Math.floor(cursor / 2);
  }
  return path;
}

export function buildMerkleProof(leaves: readonly string[], index: number): MerkleProof {
  return {
    leaf: leaves[index],
    leafIndex: index,
    treeHeight: leaves.length,
    path: getMerklePath(leaves, index),
    root: computeMerkleRoot(leaves),
  };
}

export function verifyMerkleProof(proof: MerkleProof): boolean {
  let current = proof.leaf;
  for (const step of proof.path) {
    current =
      step.position === 'left' ? hashPair(step.hash, current) : hashPair(current, step.hash);
  }
  return current === proof.root;
}
