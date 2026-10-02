export class WeightedGraph {
  private adj = new Map<string, Map<string, number>>();

  addNode(id: string): void {
    if (!this.adj.has(id)) this.adj.set(id, new Map());
  }

  addEdge(u: string, v: string, weight = 1): void {
    if (weight <= 0) throw new Error(`edge weight must be positive, got ${weight}`);
    this.addNode(u);
    this.addNode(v);
    this.adj.get(u)!.set(v, weight);
    this.adj.get(v)!.set(u, weight);
  }

  setWeight(u: string, v: string, weight: number): void {
    if (!this.hasEdge(u, v)) throw new Error(`no edge between ${u} and ${v}`);
    this.addEdge(u, v, weight);
  }

  removeEdge(u: string, v: string): void {
    this.adj.get(u)?.delete(v);
    this.adj.get(v)?.delete(u);
  }

  removeNode(id: string): Map<string, number> {
    const neighbors = this.adj.get(id);
    if (!neighbors) throw new Error(`unknown node ${id}`);
    const snapshot = new Map(neighbors);
    for (const n of snapshot.keys()) this.adj.get(n)!.delete(id);
    this.adj.delete(id);
    return snapshot;
  }

  restoreNode(id: string, neighbors: Map<string, number>): void {
    this.addNode(id);
    for (const [n, w] of neighbors) this.addEdge(id, n, w);
  }

  hasNode(id: string): boolean {
    return this.adj.has(id);
  }

  hasEdge(u: string, v: string): boolean {
    return this.adj.get(u)?.has(v) ?? false;
  }

  neighbors(id: string): Map<string, number> {
    const neighbors = this.adj.get(id);
    if (!neighbors) throw new Error(`unknown node ${id}`);
    return neighbors;
  }

  nodes(): string[] {
    return [...this.adj.keys()];
  }

  nodeCount(): number {
    return this.adj.size;
  }
}
