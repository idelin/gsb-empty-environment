/**
 * 一致性哈希环：底层索引使用红黑树（src/redBlackTree.ts）。
 * 每个物理节点映射为若干虚拟节点（vnode）散落在环上，
 * 路由 = 顺时针找到第一个位置 >= hash(key) 的虚拟节点，越界则回绕到环首。
 */
import { RedBlackTree } from "./redBlackTree.ts";

/** FNV-1a 32 位哈希，纯内置实现，无外部依赖。 */
export function fnv1a32(input: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

const uint32Compare = (a: number, b: number): number => (a < b ? -1 : a > b ? 1 : 0);

export class HashRing {
  private readonly tree = new RedBlackTree<number, string>(uint32Compare);
  private readonly vnodeCount: number;
  private readonly nodePositions = new Map<string, number[]>();

  constructor(vnodeCount = 160) {
    this.vnodeCount = vnodeCount;
  }

  get nodeCount(): number {
    return this.nodePositions.size;
  }

  get nodes(): string[] {
    return [...this.nodePositions.keys()];
  }

  private place(seed: string): number {
    let position = fnv1a32(seed);
    // 哈希位置冲突时确定性探测，保证虚拟节点不丢失。
    while (this.tree.has(position)) {
      position = fnv1a32(`${seed}#probe:${position}`);
    }
    return position;
  }

  addNode(nodeId: string): void {
    if (this.nodePositions.has(nodeId)) {
      throw new Error(`node already exists on ring: ${nodeId}`);
    }
    const positions: number[] = [];
    for (let i = 0; i < this.vnodeCount; i++) {
      const position = this.place(`${nodeId}#vnode:${i}`);
      this.tree.insert(position, nodeId);
      positions.push(position);
    }
    this.nodePositions.set(nodeId, positions);
  }

  removeNode(nodeId: string): boolean {
    const positions = this.nodePositions.get(nodeId);
    if (positions === undefined) return false;
    for (const position of positions) {
      this.tree.delete(position);
    }
    this.nodePositions.delete(nodeId);
    return true;
  }

  /** 顺时针路由：返回负责 key 的物理节点 ID。 */
  route(key: string): string {
    const hit = this.tree.ceiling(fnv1a32(key)) ?? this.tree.min();
    if (hit === null) {
      throw new Error("hash ring is empty");
    }
    return hit.value;
  }
}
