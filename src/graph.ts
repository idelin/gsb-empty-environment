/**
 * 无向稀疏图：邻接集（Set）存储，节点为 0..n-1 的稠密整数 id。
 * 不支持自环与重边。
 */
export class Graph {
  private adj: Set<number>[] = [];
  private edgeCount = 0;

  get nodeCount(): number {
    return this.adj.length;
  }

  get numEdges(): number {
    return this.edgeCount;
  }

  /** 确保节点存在；不传 id 时追加一个新节点并返回其 id。 */
  addNode(id?: number): number {
    if (id === undefined) {
      this.adj.push(new Set());
      return this.adj.length - 1;
    }
    if (!Number.isInteger(id) || id < 0) throw new Error(`invalid node id: ${id}`);
    while (this.adj.length <= id) this.adj.push(new Set());
    return id;
  }

  hasNode(id: number): boolean {
    return Number.isInteger(id) && id >= 0 && id < this.adj.length;
  }

  /** 插入一条无向边；已存在时返回 false（幂等）。 */
  addEdge(u: number, v: number): boolean {
    if (u === v) throw new Error("self loops are not supported");
    this.addNode(u);
    this.addNode(v);
    if (this.adj[u].has(v)) return false;
    this.adj[u].add(v);
    this.adj[v].add(u);
    this.edgeCount += 1;
    return true;
  }

  /** 删除一条无向边；不存在时返回 false（幂等）。 */
  removeEdge(u: number, v: number): boolean {
    if (!this.hasNode(u) || !this.hasNode(v)) return false;
    if (!this.adj[u].delete(v)) return false;
    this.adj[v].delete(u);
    this.edgeCount -= 1;
    return true;
  }

  hasEdge(u: number, v: number): boolean {
    return this.hasNode(u) && this.hasNode(v) && this.adj[u].has(v);
  }

  neighbors(id: number): ReadonlySet<number> {
    if (!this.hasNode(id)) throw new Error(`unknown node: ${id}`);
    return this.adj[id];
  }

  degree(id: number): number {
    return this.neighbors(id).size;
  }

  *nodes(): IterableIterator<number> {
    for (let i = 0; i < this.adj.length; i += 1) yield i;
  }

  *edges(): IterableIterator<[number, number]> {
    for (let u = 0; u < this.adj.length; u += 1) {
      for (const v of this.adj[u]) {
        if (u < v) yield [u, v];
      }
    }
  }
}
