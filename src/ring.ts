import { hashKey } from "./hash.ts";

interface VNode {
  point: number;
  nodeId: string;
}

/**
 * 一致性哈希环。每个物理节点按权重放置对应数量的虚拟节点，
 * 权重动态变化时仅重建受影响的虚拟节点，键按顺时针最近虚拟节点路由。
 */
export class ConsistentHashRing {
  private vnodes: VNode[] = [];
  private weights = new Map<string, number>();

  /** 设置（或更新）节点权重；权重即虚拟节点数量。0 表示摘除节点。 */
  setNodeWeight(nodeId: string, weight: number): void {
    if (!Number.isInteger(weight) || weight < 0) {
      throw new Error(`invalid weight for node ${nodeId}: ${weight}`);
    }
    if (weight === 0) {
      this.weights.delete(nodeId);
    } else {
      this.weights.set(nodeId, weight);
    }
    this.rebuild();
  }

  getWeight(nodeId: string): number {
    return this.weights.get(nodeId) ?? 0;
  }

  getVNodeCount(nodeId: string): number {
    return this.weights.get(nodeId) ?? 0;
  }

  get size(): number {
    return this.vnodes.length;
  }

  /** 路由：返回键顺时针方向的第一个虚拟节点所属的物理节点。 */
  getNode(key: string): string | undefined {
    if (this.vnodes.length === 0) return undefined;
    const point = hashKey(key);
    let lo = 0;
    let hi = this.vnodes.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.vnodes[mid].point < point) lo = mid + 1;
      else hi = mid;
    }
    return this.vnodes[lo % this.vnodes.length].nodeId;
  }

  private rebuild(): void {
    const vnodes: VNode[] = [];
    for (const [nodeId, weight] of this.weights) {
      for (let i = 0; i < weight; i++) {
        vnodes.push({ point: hashKey(`${nodeId}#vnode-${i}`), nodeId });
      }
    }
    vnodes.sort((a, b) => a.point - b.point);
    this.vnodes = vnodes;
  }
}
