import { hash32, HASH_SPACE } from "./hash.ts";

/** 网络拓扑标签：描述节点在机房网络中的物理位置。 */
export interface TopologyLabels {
  region?: string;
  zone?: string;
  rack?: string;
}

/** 物理节点描述。weight 影响虚拟节点数量（默认 1）。 */
export interface RingNode {
  id: string;
  weight?: number;
  topology?: TopologyLabels;
}

interface VNode {
  hash: number;
  nodeId: string;
}

export interface RingOptions {
  /** 每个单位权重对应的虚拟节点数，默认 160。 */
  vnodesPerWeight?: number;
}

/**
 * 网络拓扑感知的一致性哈希环。
 *
 * - 键 → 节点的映射通过虚拟节点 + 二分查找完成，增删节点只影响相邻弧段；
 * - getReplicas() 在顺时针选取副本时优先跨 zone 分散，实现拓扑感知容灾；
 * - 环结构不可变式更新：addNode/removeNode 返回新环，便于主备双环各自演进。
 */
export class ConsistentHashRing {
  private readonly vnodesPerWeight: number;
  private readonly sorted: VNode[];
  private readonly nodes: Map<string, RingNode>;

  constructor(nodes: RingNode[] = [], options: RingOptions = {}) {
    this.vnodesPerWeight = options.vnodesPerWeight ?? 160;
    this.nodes = new Map(nodes.map((n) => [n.id, n]));
    this.sorted = [];
    for (const node of nodes) {
      this.appendVNodes(node);
    }
    this.sorted.sort((a, b) => a.hash - b.hash);
  }

  /** 私有构造：由既有 vnode 列表与节点表直接构建（用于 clone / 派生）。 */
  private static fromParts(
    nodes: Map<string, RingNode>,
    sorted: VNode[],
    vnodesPerWeight: number,
  ): ConsistentHashRing {
    const ring = Object.create(ConsistentHashRing.prototype) as ConsistentHashRing;
    (ring as { vnodesPerWeight: number }).vnodesPerWeight = vnodesPerWeight;
    (ring as { nodes: Map<string, RingNode> }).nodes = nodes;
    (ring as { sorted: VNode[] }).sorted = sorted;
    return ring;
  }

  private appendVNodes(node: RingNode): void {
    const weight = Math.max(1, Math.floor(node.weight ?? 1));
    const count = this.vnodesPerWeight * weight;
    for (let i = 0; i < count; i++) {
      this.sorted.push({ hash: hash32(`${node.id}#${i}`), nodeId: node.id });
    }
  }

  /** 返回追加了节点的新环（原环不变）。 */
  addNode(node: RingNode): ConsistentHashRing {
    if (this.nodes.has(node.id)) {
      throw new Error(`node already exists: ${node.id}`);
    }
    const nodes = new Map(this.nodes);
    nodes.set(node.id, node);
    const added: VNode[] = [];
    const weight = Math.max(1, Math.floor(node.weight ?? 1));
    const count = this.vnodesPerWeight * weight;
    for (let i = 0; i < count; i++) {
      added.push({ hash: hash32(`${node.id}#${i}`), nodeId: node.id });
    }
    const sorted = this.sorted.concat(added);
    sorted.sort((a, b) => a.hash - b.hash);
    return ConsistentHashRing.fromParts(nodes, sorted, this.vnodesPerWeight);
  }

  /** 返回移除了节点的新环（原环不变）。 */
  removeNode(nodeId: string): ConsistentHashRing {
    if (!this.nodes.has(nodeId)) {
      throw new Error(`node not found: ${nodeId}`);
    }
    const nodes = new Map(this.nodes);
    nodes.delete(nodeId);
    const sorted = this.sorted.filter((v) => v.nodeId !== nodeId);
    return ConsistentHashRing.fromParts(nodes, sorted, this.vnodesPerWeight);
  }

  hasNode(nodeId: string): boolean {
    return this.nodes.has(nodeId);
  }

  getNode(nodeId: string): RingNode | undefined {
    return this.nodes.get(nodeId);
  }

  get size(): number {
    return this.nodes.size;
  }

  get vnodeCount(): number {
    return this.sorted.length;
  }

  nodeIds(): string[] {
    return [...this.nodes.keys()];
  }

  /** 二分查找：哈希值 >= key 的第一个 vnode 下标（不取模）。 */
  private lowerBound(hash: number): number {
    let lo = 0;
    let hi = this.sorted.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.sorted[mid].hash < hash) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    return lo;
  }

  /** 路由单个键到其主节点。环为空时返回 undefined。 */
  getNodeForKey(key: string): string | undefined {
    if (this.sorted.length === 0) return undefined;
    const idx = this.lowerBound(hash32(key)) % this.sorted.length;
    return this.sorted[idx].nodeId;
  }

  /**
   * 拓扑感知副本选择：从键的落点顺时针扫描，
   * 优先挑选分属不同 zone 的物理节点；zone 不足时再放宽限制补齐。
   */
  getReplicas(key: string, count: number): string[] {
    if (count <= 0 || this.sorted.length === 0) return [];
    const target = Math.min(count, this.nodes.size);
    const start = this.lowerBound(hash32(key)) % this.sorted.length;

    const picked: string[] = [];
    const pickedSet = new Set<string>();
    const usedZones = new Set<string>();
    const deferred: string[] = [];

    for (let step = 0; step < this.sorted.length && picked.length < target; step++) {
      const vnode = this.sorted[(start + step) % this.sorted.length];
      if (pickedSet.has(vnode.nodeId)) continue;
      pickedSet.add(vnode.nodeId);
      const zone = this.nodes.get(vnode.nodeId)?.topology?.zone;
      if (zone === undefined || !usedZones.has(zone)) {
        picked.push(vnode.nodeId);
        if (zone !== undefined) usedZones.add(zone);
      } else {
        deferred.push(vnode.nodeId);
      }
    }
    for (const nodeId of deferred) {
      if (picked.length >= target) break;
      picked.push(nodeId);
    }
    return picked;
  }

  /**
   * 估算两环之间发生归属变更的哈希空间比例。
   * 通过确定性均匀采样实现，无需枚举真实键。
   */
  estimateMovedFraction(other: ConsistentHashRing, samples = 4096): number {
    if (this.sorted.length === 0 && other.sorted.length === 0) return 0;
    let moved = 0;
    for (let i = 0; i < samples; i++) {
      const probe = `__probe__${i}`;
      if (this.getNodeForKey(probe) !== other.getNodeForKey(probe)) {
        moved++;
      }
    }
    return moved / samples;
  }
}

export { HASH_SPACE };
