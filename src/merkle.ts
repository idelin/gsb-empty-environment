/**
 * 分块默克尔树。
 *
 * - 叶子：sha256(chunk)（分块哈希直接作为叶子）
 * - 内部节点：sha256(0x01 || left || right)，带域分离前缀
 * - 奇数节点直接晋升到上一层（不复制）
 */
import { createHash } from "node:crypto";

export interface MerkleTree {
  root: string;
  leaves: string[];
  levels: string[][]; // levels[0] = 叶子层，最后一层 = [root]
}

function hashNode(leftHex: string, rightHex: string): string {
  const h = createHash("sha256");
  h.update(Buffer.from([0x01]));
  h.update(Buffer.from(leftHex, "hex"));
  h.update(Buffer.from(rightHex, "hex"));
  return h.digest("hex");
}

export function buildMerkleTree(leaves: string[]): MerkleTree {
  if (leaves.length === 0) throw new Error("merkle tree requires >= 1 leaf");
  const levels: string[][] = [leaves.slice()];
  let current = leaves.slice();
  while (current.length > 1) {
    const next: string[] = [];
    for (let i = 0; i < current.length; i += 2) {
      if (i + 1 < current.length) next.push(hashNode(current[i], current[i + 1]));
      else next.push(current[i]); // 奇数晋升
    }
    levels.push(next);
    current = next;
  }
  return { root: current[0], leaves: leaves.slice(), levels };
}

/** 返回叶子 index 的默克尔证明路径（兄弟哈希 + 左右标记）。 */
export function merkleProof(
  tree: MerkleTree,
  index: number
): Array<{ hash: string; position: "left" | "right" }> {
  const proof: Array<{ hash: string; position: "left" | "right" }> = [];
  let i = index;
  for (let level = 0; level < tree.levels.length - 1; level++) {
    const nodes = tree.levels[level];
    const sibling = i % 2 === 0 ? i + 1 : i - 1;
    if (sibling < nodes.length) {
      proof.push({
        hash: nodes[sibling],
        position: i % 2 === 0 ? "right" : "left",
      });
    }
    i = Math.floor(i / 2);
  }
  return proof;
}

export function verifyProof(
  leafHash: string,
  proof: Array<{ hash: string; position: "left" | "right" }>,
  root: string
): boolean {
  let acc = leafHash;
  for (const step of proof) {
    acc =
      step.position === "right"
        ? hashNode(acc, step.hash)
        : hashNode(step.hash, acc);
  }
  return acc === root;
}

/**
 * 对比两棵树的叶子序列，找出发生变化的叶子区间。
 * 返回共同前缀/后缀长度，以及新旧树中受影响的叶子下标区间。
 */
export function diffLeaves(
  oldLeaves: string[],
  newLeaves: string[]
): {
  commonPrefix: number;
  commonSuffix: number;
  oldChanged: [number, number]; // [start, end)
  newChanged: [number, number];
} {
  let prefix = 0;
  const maxPrefix = Math.min(oldLeaves.length, newLeaves.length);
  while (prefix < maxPrefix && oldLeaves[prefix] === newLeaves[prefix]) prefix++;

  let suffix = 0;
  while (
    suffix < oldLeaves.length - prefix &&
    suffix < newLeaves.length - prefix &&
    oldLeaves[oldLeaves.length - 1 - suffix] ===
      newLeaves[newLeaves.length - 1 - suffix]
  ) {
    suffix++;
  }
  return {
    commonPrefix: prefix,
    commonSuffix: suffix,
    oldChanged: [prefix, oldLeaves.length - suffix],
    newChanged: [prefix, newLeaves.length - suffix],
  };
}

/**
 * 计算受影响的默克尔路径：从变化叶子到根沿途、
 * 在新树中值与旧树不同（或新出现）的节点数量。
 */
export function changedMerkleNodes(
  oldTree: MerkleTree,
  newTree: MerkleTree
): number {
  let changed = 0;
  const levels = Math.max(oldTree.levels.length, newTree.levels.length);
  for (let l = 0; l < levels; l++) {
    const a = oldTree.levels[l] ?? [];
    const b = newTree.levels[l] ?? [];
    const n = Math.max(a.length, b.length);
    for (let i = 0; i < n; i++) {
      if (a[i] !== b[i]) changed++;
    }
  }
  return changed;
}
