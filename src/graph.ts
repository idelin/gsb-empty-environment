export type NodeId = string;

/** 无向加权图，支持边权动态变动。 */
export class WeightedGraph {
  private adj = new Map<NodeId, Map<NodeId, number>>();

  addNode(node: NodeId): void {
    if (!this.adj.has(node)) this.adj.set(node, new Map());
  }

  addEdge(u: NodeId, v: NodeId, weight = 1): void {
    if (u === v) throw new Error(`self-loop not allowed: ${u}`);
    if (!(weight > 0)) throw new Error(`edge weight must be positive: ${weight}`);
    this.addNode(u);
    this.addNode(v);
    this.adj.get(u)!.set(v, weight);
    this.adj.get(v)!.set(u, weight);
  }

  updateEdgeWeight(u: NodeId, v: NodeId, weight: number): void {
    if (!this.hasEdge(u, v)) throw new Error(`edge not found: ${u}-${v}`);
    this.addEdge(u, v, weight);
  }

  removeEdge(u: NodeId, v: NodeId): void {
    this.adj.get(u)?.delete(v);
    this.adj.get(v)?.delete(u);
  }

  removeNode(node: NodeId): void {
    for (const nbr of this.neighbors(node).keys()) this.adj.get(nbr)?.delete(node);
    this.adj.delete(node);
  }

  hasNode(node: NodeId): boolean {
    return this.adj.has(node);
  }

  hasEdge(u: NodeId, v: NodeId): boolean {
    return this.adj.get(u)?.has(v) ?? false;
  }

  weight(u: NodeId, v: NodeId): number | undefined {
    return this.adj.get(u)?.get(v);
  }

  neighbors(node: NodeId): Map<NodeId, number> {
    return this.adj.get(node) ?? new Map();
  }

  nodes(): NodeId[] {
    return [...this.adj.keys()];
  }

  get nodeCount(): number {
    return this.adj.size;
  }

  get edgeCount(): number {
    let sum = 0;
    for (const nbrs of this.adj.values()) sum += nbrs.size;
    return sum / 2;
  }

  clone(): WeightedGraph {
    const g = new WeightedGraph();
    for (const node of this.adj.keys()) g.addNode(node);
    for (const [u, nbrs] of this.adj) {
      for (const [v, w] of nbrs) {
        if (u < v) g.addEdge(u, v, w);
      }
    }
    return g;
  }
}
