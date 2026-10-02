import { sha256Hex } from './crypto.ts';

export interface MerkleProofStep {
  position: 'left' | 'right';
  hash: string;
}

export interface MerkleProof {
  leafIndex: number;
  leafHash: string;
  path: MerkleProofStep[];
  root: string;
}

function hashPair(left: string, right: string): string {
  return sha256Hex(left + right);
}

export function buildMerkleLayers(leaves: string[]): string[][] {
  if (leaves.length === 0) {
    throw new Error('cannot build a Merkle tree with no leaves');
  }
  const layers: string[][] = [leaves.slice()];
  let current = leaves.slice();
  while (current.length > 1) {
    const next: string[] = [];
    for (let i = 0; i < current.length; i += 2) {
      const left = current[i];
      const right = i + 1 < current.length ? current[i + 1] : current[i];
      next.push(hashPair(left, right));
    }
    layers.push(next);
    current = next;
  }
  return layers;
}

export function merkleRoot(leaves: string[]): string {
  const layers = buildMerkleLayers(leaves);
  return layers[layers.length - 1][0];
}

export function generateProof(leaves: string[], leafIndex: number): MerkleProof {
  if (leafIndex < 0 || leafIndex >= leaves.length) {
    throw new RangeError(`leaf index ${leafIndex} out of range`);
  }
  const layers = buildMerkleLayers(leaves);
  const path: MerkleProofStep[] = [];
  let index = leafIndex;
  for (let level = 0; level < layers.length - 1; level++) {
    const layer = layers[level];
    const isRightNode = index % 2 === 1;
    const siblingIndex = isRightNode ? index - 1 : index + 1;
    const sibling =
      siblingIndex < layer.length ? layer[siblingIndex] : layer[index];
    path.push({
      position: isRightNode ? 'left' : 'right',
      hash: sibling,
    });
    index = Math.floor(index / 2);
  }
  return {
    leafIndex,
    leafHash: leaves[leafIndex],
    path,
    root: layers[layers.length - 1][0],
  };
}

export function verifyProof(proof: MerkleProof, expectedRoot?: string): boolean {
  let computed = proof.leafHash;
  for (const step of proof.path) {
    computed =
      step.position === 'left'
        ? hashPair(step.hash, computed)
        : hashPair(computed, step.hash);
  }
  const root = expectedRoot ?? proof.root;
  return computed === root;
}
