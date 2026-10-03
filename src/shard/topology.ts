/**
 * 分层网络拓扑：DC（数据中心）→ Rack（机架）→ Node（节点）。
 * 网络距离用于局部性感知调度：
 *   同节点=0，同机架=1，同数据中心=2，跨数据中心=3。
 */

export interface NodeInfo {
  nodeId: string;
  rackId: string;
  dcId: string;
  /** 相对容量权重 */
  capacity: number;
}

export const LOCALITY = {
  SAME_NODE: 0,
  SAME_RACK: 1,
  SAME_DC: 2,
  CROSS_DC: 3,
} as const;

export class Topology {
  private nodes = new Map<string, NodeInfo>();

  addNode(node: NodeInfo): void {
    if (this.nodes.has(node.nodeId)) {
      throw new Error(`节点已存在: ${node.nodeId}`);
    }
    this.nodes.set(node.nodeId, { ...node });
  }

  removeNode(nodeId: string): void {
    if (!this.nodes.delete(nodeId)) {
      throw new Error(`节点不存在: ${nodeId}`);
    }
  }

  has(nodeId: string): boolean {
    return this.nodes.has(nodeId);
  }

  get(nodeId: string): NodeInfo {
    const node = this.nodes.get(nodeId);
    if (!node) throw new Error(`节点不存在: ${nodeId}`);
    return { ...node };
  }

  allNodes(): NodeInfo[] {
    return [...this.nodes.values()].map((n) => ({ ...n }));
  }

  get size(): number {
    return this.nodes.size;
  }

  /** 两节点间网络距离（分层局部性度量） */
  distance(a: string, b: string): number {
    if (a === b) return LOCALITY.SAME_NODE;
    const na = this.nodes.get(a);
    const nb = this.nodes.get(b);
    if (!na || !nb) throw new Error(`节点不存在: ${!na ? a : b}`);
    if (na.rackId === nb.rackId && na.dcId === nb.dcId) return LOCALITY.SAME_RACK;
    if (na.dcId === nb.dcId) return LOCALITY.SAME_DC;
    return LOCALITY.CROSS_DC;
  }

  /** 某局部域内的全部节点 */
  nodesInScope(scope: { dcId?: string; rackId?: string }): NodeInfo[] {
    return this.allNodes().filter(
      (n) =>
        (scope.dcId === undefined || n.dcId === scope.dcId) &&
        (scope.rackId === undefined || n.rackId === scope.rackId),
    );
  }
}
