/** 无向图（邻接表表示，节点为整数编号）。 */
export class Graph {
  private adj = new Map<number, Set<number>>();

  addNode(u: number): void {
    if (!this.adj.has(u)) this.adj.set(u, new Set());
  }

  hasNode(u: number): boolean {
    return this.adj.has(u);
  }

  addEdge(u: number, v: number): void {
    if (u === v) throw new Error(`不允许自环边 (${u}, ${v})`);
    this.addNode(u);
    this.addNode(v);
    this.adj.get(u)!.add(v);
    this.adj.get(v)!.add(u);
  }

  removeEdge(u: number, v: number): void {
    this.adj.get(u)?.delete(v);
    this.adj.get(v)?.delete(u);
  }

  hasEdge(u: number, v: number): boolean {
    return this.adj.get(u)?.has(v) ?? false;
  }

  neighbors(u: number): ReadonlySet<number> {
    return this.adj.get(u) ?? new Set();
  }

  nodes(): number[] {
    return [...this.adj.keys()];
  }

  edgeCount(): number {
    let sum = 0;
    for (const s of this.adj.values()) sum += s.size;
    return sum / 2;
  }

  nodeCount(): number {
    return this.adj.size;
  }
}
