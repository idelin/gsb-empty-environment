/**
 * 无向图：邻接表实现（基于 Set，支持动态增删边/点）。
 */
export class Graph {
  private adj: Map<number, Set<number>> = new Map();

  addVertex(v: number): void {
    if (!this.adj.has(v)) this.adj.set(v, new Set());
  }

  removeVertex(v: number): void {
    const ns = this.adj.get(v);
    if (!ns) return;
    for (const u of ns) this.adj.get(u)!.delete(v);
    this.adj.delete(v);
  }

  addEdge(u: number, v: number): void {
    if (u === v) throw new Error(`自环非法: ${u}`);
    this.addVertex(u);
    this.addVertex(v);
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

  hasVertex(v: number): boolean {
    return this.adj.has(v);
  }

  neighbors(v: number): Set<number> {
    return this.adj.get(v) ?? new Set();
  }

  degree(v: number): number {
    return this.adj.get(v)?.size ?? 0;
  }

  vertices(): number[] {
    return [...this.adj.keys()];
  }

  get vertexCount(): number {
    return this.adj.size;
  }

  get edgeCount(): number {
    let sum = 0;
    for (const ns of this.adj.values()) sum += ns.size;
    return sum / 2;
  }

  /** 顶点集 S 是否构成团（两两相邻）。 */
  isClique(s: Iterable<number>): boolean {
    const arr = [...s];
    for (let i = 0; i < arr.length; i++) {
      const ns = this.adj.get(arr[i]);
      if (!ns) return false;
      for (let j = i + 1; j < arr.length; j++) {
        if (!ns.has(arr[j])) return false;
      }
    }
    return true;
  }
}
