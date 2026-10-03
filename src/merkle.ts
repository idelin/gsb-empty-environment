import { sha256Hex } from "./hash.ts";

export interface MerkleProofStep {
  /** 兄弟哈希在当前节点的左侧还是右侧。 */
  position: "left" | "right";
  hash: string;
}

/** 简单 Merkle 树：叶子为各轨迹点哈希，奇数节点直接晋升上一层。 */
export class MerkleTree {
  readonly leaves: string[];
  readonly root: string;
  private readonly levels: string[][];

  constructor(leaves: string[]) {
    if (leaves.length === 0) {
      this.leaves = [];
      this.levels = [[]];
      this.root = sha256Hex("EMPTY-TREE");
      return;
    }
    this.leaves = [...leaves];
    this.levels = [[...leaves]];
    let current = [...leaves];
    while (current.length > 1) {
      const next: string[] = [];
      for (let i = 0; i < current.length; i += 2) {
        if (i + 1 < current.length) {
          next.push(sha256Hex(current[i] + current[i + 1]));
        } else {
          next.push(current[i]);
        }
      }
      this.levels.push(next);
      current = next;
    }
    this.root = current[0];
  }

  proof(index: number): MerkleProofStep[] {
    if (index < 0 || index >= this.leaves.length) {
      throw new RangeError(`leaf index out of range: ${index}`);
    }
    const steps: MerkleProofStep[] = [];
    let i = index;
    for (let level = 0; level < this.levels.length - 1; level++) {
      const nodes = this.levels[level];
      const sibling = i % 2 === 0 ? i + 1 : i - 1;
      if (sibling < nodes.length) {
        steps.push({
          position: i % 2 === 0 ? "right" : "left",
          hash: nodes[sibling],
        });
      }
      i = Math.floor(i / 2);
    }
    return steps;
  }

  static verify(leafHash: string, proof: MerkleProofStep[], root: string): boolean {
    let acc = leafHash;
    for (const step of proof) {
      acc =
        step.position === "left"
          ? sha256Hex(step.hash + acc)
          : sha256Hex(acc + step.hash);
    }
    return acc === root;
  }
}
