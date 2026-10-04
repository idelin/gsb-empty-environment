import type { WarehouseGraph } from './types.ts';

/** 全源最短路（Floyd-Warshall）+ 路径还原 */
export class DistanceTable {
  private readonly index: Map<string, number>;
  private readonly dist: number[][];
  private readonly next: number[][];

  constructor(graph: WarehouseGraph) {
    const n = graph.nodes.length;
    this.index = new Map(graph.nodes.map((node, i) => [node, i]));
    const inf = Number.POSITIVE_INFINITY;
    this.dist = Array.from({ length: n }, (_, i) =>
      Array.from({ length: n }, (_, j) => (i === j ? 0 : inf)),
    );
    this.next = Array.from({ length: n }, () => new Array<number>(n).fill(-1));
    for (const e of graph.edges) {
      const u = this.idx(e.from);
      const v = this.idx(e.to);
      if (e.dist < this.dist[u][v]) {
        this.dist[u][v] = e.dist;
        this.dist[v][u] = e.dist;
        this.next[u][v] = v;
        this.next[v][u] = u;
      }
    }
    for (let k = 0; k < n; k++) {
      for (let i = 0; i < n; i++) {
        const dik = this.dist[i][k];
        if (!Number.isFinite(dik)) continue;
        for (let j = 0; j < n; j++) {
          const alt = dik + this.dist[k][j];
          if (alt < this.dist[i][j]) {
            this.dist[i][j] = alt;
            this.next[i][j] = this.next[i][k];
          }
        }
      }
    }
  }

  private idx(node: string): number {
    const i = this.index.get(node);
    if (i === undefined) throw new Error(`未知节点: ${node}`);
    return i;
  }

  distance(from: string, to: string): number {
    const d = this.dist[this.idx(from)][this.idx(to)];
    if (!Number.isFinite(d)) throw new Error(`节点不连通: ${from} -> ${to}`);
    return d;
  }

  /** 还原 from->to 的最短路径节点序列（含两端） */
  path(from: string, to: string): string[] {
    const nodes = [...this.index.keys()];
    let u = this.idx(from);
    const v = this.idx(to);
    if (this.next[u][v] === -1 && u !== v) throw new Error(`节点不连通: ${from} -> ${to}`);
    const out = [nodes[u]];
    while (u !== v) {
      u = this.next[u][v];
      out.push(nodes[u]);
    }
    return out;
  }
}
