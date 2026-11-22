export interface MerkleProofNode {
  /** 0 is the sibling of the leaf, 31 is the sibling below the root. */
  readonly level: number;
  readonly hash: string;
  /** Left/right describes where the sibling sits, not the current node. */
  readonly position: "left" | "right";
}

export interface MerkleProof {
  readonly key: string;
  readonly leaf: string;
  readonly root: string;
  readonly depth: number;
  readonly path: string;
  readonly siblings: ReadonlyArray<MerkleProofNode>;
}
