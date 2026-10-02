import { createHash } from "node:crypto";

export function sha256(data: Uint8Array | string): Buffer {
  return createHash("sha256").update(data).digest();
}

export function hashLeaf(data: Uint8Array | string): Buffer {
  const payload = typeof data === "string" ? Buffer.from(data, "utf8") : data;
  return sha256(Buffer.concat([Buffer.from([0x00]), payload]));
}

export function hashNode(left: Buffer, right: Buffer): Buffer {
  return sha256(Buffer.concat([Buffer.from([0x01]), left, right]));
}

export interface ProofStep {
  position: "left" | "right";
  hash: string;
}

export interface MerkleTree {
  root: Buffer;
  leaves: Buffer[];
  levels: Buffer[][];
}

export function buildTree(leaves: Buffer[]): MerkleTree {
  if (leaves.length === 0) {
    throw new Error("cannot build a Merkle tree with zero leaves");
  }
  const levels: Buffer[][] = [leaves.slice()];
  let current = leaves.slice();
  while (current.length > 1) {
    const next: Buffer[] = [];
    for (let i = 0; i < current.length; i += 2) {
      const left = current[i];
      const right = i + 1 < current.length ? current[i + 1] : current[i];
      next.push(hashNode(left, right));
    }
    levels.push(next);
    current = next;
  }
  return { root: current[0], leaves: leaves.slice(), levels };
}

export function getProof(tree: MerkleTree, leafIndex: number): ProofStep[] {
  if (leafIndex < 0 || leafIndex >= tree.leaves.length) {
    throw new Error(`leaf index ${leafIndex} out of range`);
  }
  const proof: ProofStep[] = [];
  let index = leafIndex;
  for (let level = 0; level < tree.levels.length - 1; level++) {
    const nodes = tree.levels[level];
    const siblingIndex = index % 2 === 0 ? index + 1 : index - 1;
    const sibling =
      siblingIndex < nodes.length ? nodes[siblingIndex] : nodes[index];
    proof.push({
      position: index % 2 === 0 ? "right" : "left",
      hash: sibling.toString("hex"),
    });
    index = Math.floor(index / 2);
  }
  return proof;
}

export function verifyProof(
  leafHash: Buffer,
  proof: ProofStep[],
  expectedRoot: Buffer,
): boolean {
  let current = leafHash;
  for (const step of proof) {
    const sibling = Buffer.from(step.hash, "hex");
    current =
      step.position === "right"
        ? hashNode(current, sibling)
        : hashNode(sibling, current);
  }
  return current.equals(expectedRoot);
}
