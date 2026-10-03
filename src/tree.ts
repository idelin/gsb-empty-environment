import { hashNode, type Hex } from "./hash.ts";

export interface ProofStep {
  position: "left" | "right";
  hash: Hex;
}

export interface MerkleProof {
  leafIndex: number;
  leafHash: Hex;
  path: ProofStep[];
  root: Hex;
}

export function buildLevels(leaves: Hex[]): Hex[][] {
  if (leaves.length === 0) throw new Error("tree must have at least one leaf");
  const levels: Hex[][] = [leaves.slice()];
  let current = leaves;
  while (current.length > 1) {
    const next: Hex[] = [];
    for (let i = 0; i < current.length; i += 2) {
      const left = current[i];
      const right = i + 1 < current.length ? current[i + 1] : current[i];
      next.push(hashNode(left, right));
    }
    levels.push(next);
    current = next;
  }
  return levels;
}

export function rootOf(leaves: Hex[]): Hex {
  const levels = buildLevels(leaves);
  return levels[levels.length - 1][0];
}

export function buildProof(leaves: Hex[], leafIndex: number): MerkleProof {
  if (leafIndex < 0 || leafIndex >= leaves.length) {
    throw new RangeError(`leaf index ${leafIndex} out of range`);
  }
  const levels = buildLevels(leaves);
  const path: ProofStep[] = [];
  let index = leafIndex;
  for (let level = 0; level < levels.length - 1; level++) {
    const nodes = levels[level];
    const isRightNode = index % 2 === 1;
    const siblingIndex = isRightNode ? index - 1 : index + 1;
    const sibling = siblingIndex < nodes.length ? nodes[siblingIndex] : nodes[index];
    path.push({ position: isRightNode ? "left" : "right", hash: sibling });
    index = Math.floor(index / 2);
  }
  return {
    leafIndex,
    leafHash: leaves[leafIndex],
    path,
    root: levels[levels.length - 1][0],
  };
}

export function computeRootFromProof(leafHash: Hex, leafIndex: number, path: ProofStep[]): Hex {
  let hash = leafHash;
  let index = leafIndex;
  for (const step of path) {
    hash = step.position === "left" ? hashNode(step.hash, hash) : hashNode(hash, step.hash);
    index = Math.floor(index / 2);
  }
  return hash;
}

export function verifyProof(proof: MerkleProof, expectedRoot?: Hex): boolean {
  const computed = computeRootFromProof(proof.leafHash, proof.leafIndex, proof.path);
  return computed === (expectedRoot ?? proof.root);
}
