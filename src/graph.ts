/**
 * 无向简单稀疏图，邻接表存储（邻接集合），支持 O(1) 增删边。
 */
export class Graph {
  private readonly adj = new Map<number, Set<number>>();

  addNode(v: number): void {
    if (!this.adj.has(v)) this.adj.set(v, new Set());
  }

  /** 插入边；若边已存在则返回 false（幂等）。 */
  addEdge(u: number, v: number): boolean {
    if (u === v) throw new Error(`self-loop not supported: (${u}, ${v})`);
    this.addNode(u);
    this.addNode(v);
    const su = this.adj.get(u)!;
    if (su.has(v)) return false;
    su.add(v);
    this.adj.get(v)!.add(u);
    return true;
  }

  /** 删除边；若边不存在则返回 false（幂等）。 */
  removeEdge(u: number, v: number): boolean {
    const su = this.adj.get(u);
    if (su === undefined || !su.has(v)) return false;
    su.delete(v);
    this.adj.get(v)!.delete(u);
    return true;
  }

  hasEdge(u: number, v: number): boolean {
    return this.adj.get(u)?.has(v) ?? false;
  }

  hasNode(v: number): boolean {
    return this.adj.has(v);
  }

  neighbors(v: number): ReadonlySet<number> {
    return this.adj.get(v) ?? new Set<number>();
  }

  degree(v: number): number {
    return this.adj.get(v)?.size ?? 0;
  }

  nodes(): number[] {
    return [...this.adj.keys()];
  }

  get nodeCount(): number {
    return this.adj.size;
  }

  get edgeCount(): number {
    let sum = 0;
    for (const s of this.adj.values()) sum += s.size;
    return sum / 2;
  }

  edges(): Array<[number, number]> {
    const out: Array<[number, number]> = [];
    for (const [u, ns] of this.adj) {
      for (const v of ns) if (u < v) out.push([u, v]);
    }
    return out;
  }
}
