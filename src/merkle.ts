import { hashNode } from './crypto.ts';

export interface ProofStep {
  /** sibling 相对当前节点的位置：sibling 在左则为 'left'。 */
  position: 'left' | 'right';
  hash: Buffer;
}

export interface MerkleProof {
  leafIndex: number;
  leafHash: Buffer;
  path: ProofStep[];
}

/**
 *  append-only 风格的全量 Merkle 树，支持叶子原地重写（擦除场景）。
 *  奇数层节点直接晋升上一层（promote），证明生成与验证遵循同一规则。
 */
export class MerkleTree {
  private leaves: Buffer[];

  constructor(leafHashes: Buffer[]) {
    if (leafHashes.length === 0) throw new Error('MerkleTree 至少需要一片叶子');
    this.leaves = leafHashes.map((h) => Buffer.from(h));
  }

  get size(): number {
    return this.leaves.length;
  }

  leafHashAt(index: number): Buffer {
    this.assertIndex(index);
    return Buffer.from(this.leaves[index]);
  }

  /** 原地重写叶子并返回新根。只影响该叶到根的路径，其余子树不变。 */
  updateLeaf(index: number, newLeafHash: Buffer): Buffer {
    this.assertIndex(index);
    this.leaves[index] = Buffer.from(newLeafHash);
    return this.root();
  }

  root(): Buffer {
    let level = this.leaves;
    while (level.length > 1) level = MerkleTree.parentLevel(level);
    return Buffer.from(level[0]);
  }

  static parentLevel(level: Buffer[]): Buffer[] {
    const next: Buffer[] = [];
    for (let i = 0; i < level.length; i += 2) {
      if (i + 1 < level.length) next.push(hashNode(level[i], level[i + 1]));
      else next.push(level[i]); // 奇数节点晋升
    }
    return next;
  }

  /** 生成叶子的存证路径（Merkle inclusion proof）。 */
  prove(leafIndex: number): MerkleProof {
    this.assertIndex(leafIndex);
    const path: ProofStep[] = [];
    let level = this.leaves.map((h) => Buffer.from(h));
    let idx = leafIndex;
    while (level.length > 1) {
      const sibling = idx % 2 === 0 ? idx + 1 : idx - 1;
      if (sibling < level.length) {
        path.push({
          position: idx % 2 === 0 ? 'right' : 'left',
          hash: Buffer.from(level[sibling]),
        });
      }
      level = MerkleTree.parentLevel(level);
      idx = Math.floor(idx / 2);
    }
    return { leafIndex, leafHash: Buffer.from(this.leaves[leafIndex]), path };
  }

  /** 依据证明从叶子哈希重放至根，与给定根比对。 */
  static verify(proof: MerkleProof, expectedRoot: Buffer): boolean {
    let acc = proof.leafHash;
    for (const step of proof.path) {
      acc = step.position === 'left' ? hashNode(step.hash, acc) : hashNode(acc, step.hash);
    }
    return timingSafeEqual(acc, expectedRoot);
  }

  private assertIndex(index: number): void {
    if (!Number.isInteger(index) || index < 0 || index >= this.leaves.length) {
      throw new RangeError(`叶子下标越界: ${index}（共 ${this.leaves.length} 片叶子）`);
    }
  }
}

export function timingSafeEqual(a: Buffer, b: Buffer): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}
